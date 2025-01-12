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
FROM debian:bullseye-slim

# Install runtime dependencies
RUN apt update && apt install -y libc6 libsqlite3-0 && apt clean

# Copy the binary from the builder stage
COPY --from=builder /app/metrics /metrics

# Expose the port used by your application
EXPOSE 8089

# Set the entrypoint
CMD ["/metrics"]
