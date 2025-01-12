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
	"k8s.io/client-go/rest"
	metrics "k8s.io/metrics/pkg/client/clientset/versioned"
)

type MetricsData struct {
	Timestamp   time.Time `json:"timestamp"`
	NodeName    string    `json:"node_name"`
	CpuUsage    int64     `json:"cpu_usage"`
	MemoryUsage int64     `json:"memory_usage"`
	IsBenchmark bool      `json:"is_benchmark"`
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
	go collectMetrics()

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

	// Create metrics table
	_, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS metrics (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            timestamp DATETIME,
            node_name TEXT,
            cpu_usage INTEGER,
            memory_usage INTEGER,
            is_benchmark BOOLEAN
        )
    `)
	if err != nil {
		log.Fatal(err)
	}
}

func collectMetrics() {
	ticker := time.NewTicker(30 * time.Second)
	for range ticker.C {
		nodes, err := metricsClient.MetricsV1beta1().NodeMetricses().List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			log.Printf("Error collecting metrics: %v", err)
			continue
		}

		for _, node := range nodes.Items {
			_, err := db.Exec(
				"INSERT INTO metrics (timestamp, node_name, cpu_usage, memory_usage, is_benchmark) VALUES (?, ?, ?, ?, ?)",
				time.Now(),
				node.Name,
				node.Usage.Cpu().MilliValue(),
				node.Usage.Memory().Value(),
				false,
			)
			if err != nil {
				log.Printf("Error inserting metrics: %v", err)
			}
		}
	}
}

func getMetrics(c *gin.Context) {
	rows, err := db.Query("SELECT timestamp, node_name, cpu_usage, memory_usage, is_benchmark FROM metrics ORDER BY timestamp DESC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var metrics []MetricsData
	for rows.Next() {
		var m MetricsData
		err := rows.Scan(&m.Timestamp, &m.NodeName, &m.CpuUsage, &m.MemoryUsage, &m.IsBenchmark)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		metrics = append(metrics, m)
	}

	c.JSON(http.StatusOK, metrics)
}

func startBenchmark(c *gin.Context) {
	_, err := db.Exec(
		"INSERT INTO metrics (timestamp, node_name, cpu_usage, memory_usage, is_benchmark) SELECT timestamp, node_name, cpu_usage, memory_usage, 1 FROM metrics WHERE id IN (SELECT id FROM metrics ORDER BY timestamp DESC LIMIT 1)",
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusCreated)
}

func resetDB(c *gin.Context) {
	_, err := db.Exec("DELETE FROM metrics")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusOK)
}
