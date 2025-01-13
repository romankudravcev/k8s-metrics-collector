package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	metrics "k8s.io/metrics/pkg/client/clientset/versioned"
)

type MetricsData struct {
	Timestamp       time.Time `json:"timestamp"`
	NodeName        string    `json:"node_name"`
	CpuUsage        float64   `json:"cpu_usage"`
	MemoryUsage     int64     `json:"memory_usage"`
	IsBenchmark     bool      `json:"is_benchmark"`
	ClusterCpuUsage float64   `json:"cluster_cpu_usage"`
	ClusterTotalCpu int64     `json:"cluster_total_cpu"`
}

var db *sql.DB
var metricsClient *metrics.Clientset

func main() {
	// Initialize database
	initDB()

	// Initialize Kubernetes metrics client
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatal(err)
	}

	metricsClient, err = metrics.NewForConfig(config)
	if err != nil {
		log.Fatal(err)
	}

	// Start metrics collection
	go collectMetrics(config)

	// Setup HTTP server
	router := gin.Default()
	router.GET("/metrics", getMetrics)
	router.POST("/metrics/benchmark", startBenchmark)
	router.POST("/metrics/reset", resetDB)

	log.Fatal(http.ListenAndServe(":8089", router))
}

func initDB() {
	var err error
	db, err = sql.Open("sqlite3", "./metrics.db")
	if err != nil {
		log.Fatal(err)
	}

	_, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS metrics (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            timestamp DATETIME,
            node_name TEXT,
            cpu_usage REAL,
            memory_usage INTEGER,
            is_benchmark BOOLEAN,
            cluster_cpu_usage REAL,
            cluster_total_cpu INTEGER
        )
    `)
	if err != nil {
		log.Fatal(err)
	}
}

func collectMetrics(config *rest.Config) {
	ticker := time.NewTicker(1 * time.Second)
	for range ticker.C {
		// Get node metrics
		nodes, err := metricsClient.MetricsV1beta1().NodeMetricses().List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			log.Printf("Error collecting metrics: %v", err)
			continue
		}

		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			log.Printf("Error creating clientset: %v", err)
			continue
		}

		// Calculate cluster-wide totals
		var clusterTotalCPU int64 = 0
		var clusterUsedCPU int64 = 0

		// First pass: gather cluster totals
		for _, nodeMetric := range nodes.Items {
			node, err := clientset.CoreV1().Nodes().Get(context.TODO(), nodeMetric.Name, metav1.GetOptions{})
			if err != nil {
				log.Printf("Error getting node info: %v", err)
				continue
			}

			// Add to cluster totals
			clusterTotalCPU += node.Status.Capacity.Cpu().MilliValue()
			clusterUsedCPU += nodeMetric.Usage.Cpu().MilliValue()
		}

		// Calculate cluster-wide CPU percentage
		clusterCpuPercentage := float64(clusterUsedCPU) / float64(clusterTotalCPU) * 100

		// Second pass: store metrics with cluster-wide information
		for _, nodeMetric := range nodes.Items {
			node, err := clientset.CoreV1().Nodes().Get(context.TODO(), nodeMetric.Name, metav1.GetOptions{})
			if err != nil {
				continue
			}

			nodeTotalCPU := node.Status.Capacity.Cpu().MilliValue()
			nodeUsedCPU := nodeMetric.Usage.Cpu().MilliValue()

			// Calculate individual node percentage
			nodePercentage := float64(nodeUsedCPU) / float64(nodeTotalCPU) * 100

			_, err = db.Exec(
				`INSERT INTO metrics (
                    timestamp,
                    node_name,
                    cpu_usage,
                    memory_usage,
                    is_benchmark,
                    cluster_cpu_usage,
                    cluster_total_cpu
                ) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				time.Now(),
				nodeMetric.Name,
				nodePercentage, // Individual node CPU percentage
				nodeMetric.Usage.Memory().Value(),
				false,
				clusterCpuPercentage, // Cluster-wide CPU percentage
				clusterTotalCPU,
			)
			if err != nil {
				log.Printf("Error inserting metrics: %v", err)
			}
		}
	}
}

func getMetrics(c *gin.Context) {
	rows, err := db.Query(`
        SELECT
            timestamp,
            node_name,
            cpu_usage,
            memory_usage,
            is_benchmark,
            cluster_cpu_usage,
            cluster_total_cpu
        FROM metrics
        ORDER BY timestamp DESC
    `)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var metrics []MetricsData
	for rows.Next() {
		var m MetricsData
		err := rows.Scan(
			&m.Timestamp,
			&m.NodeName,
			&m.CpuUsage,
			&m.MemoryUsage,
			&m.IsBenchmark,
			&m.ClusterCpuUsage,
			&m.ClusterTotalCpu,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		metrics = append(metrics, m)
	}
	c.JSON(http.StatusOK, metrics)
}

func startBenchmark(c *gin.Context) {
	_, err := db.Exec(`
        INSERT INTO metrics (
            timestamp,
            node_name,
            cpu_usage,
            memory_usage,
            is_benchmark,
            cluster_cpu_usage,
            cluster_total_cpu
        )
        SELECT
            timestamp,
            node_name,
            cpu_usage,
            memory_usage,
            1,
            cluster_cpu_usage,
            cluster_total_cpu
        FROM metrics
        WHERE id IN (
            SELECT id
            FROM metrics
            WHERE is_benchmark = 0
            ORDER BY timestamp DESC
            LIMIT 1
        )
    `)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusCreated)
}

func resetDB(c *gin.Context) {
	// Begin a transaction
	tx, err := db.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction: " + err.Error()})
		return
	}

	// Delete all records
	_, err = tx.Exec("DELETE FROM metrics")
	if err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete records: " + err.Error()})
		return
	}

	// Reset the auto-increment counter
	_, err = tx.Exec("DELETE FROM sqlite_sequence WHERE name='metrics'")
	if err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reset sequence: " + err.Error()})
		return
	}

	// Commit the transaction
	err = tx.Commit()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction: " + err.Error()})
		return
	}

	c.Status(http.StatusOK)
}
