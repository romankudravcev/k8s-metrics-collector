# Build stage
FROM golang:1.23.4 AS builder

WORKDIR /app

# Copy the go.mod and go.sum files to download dependencies first (if any)
COPY go.mod ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the binary
RUN go build -o metrics

# Final stage
FROM alpine:edge

# Set the working directory
WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /app/metrics /metrics

# Expose the desired port
EXPOSE 8089

# Set the entrypoint
CMD ["/metrics"]
