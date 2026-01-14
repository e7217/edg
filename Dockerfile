# Multi-stage build for EDG Core
FROM golang:1.24-bookworm AS builder

WORKDIR /build

# Install build dependencies for CGO (required for sqlite)
RUN apt-get update && apt-get install -y gcc

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application with CGO enabled for sqlite
RUN CGO_ENABLED=1 GOOS=linux go build -a -o edg-core ./cmd/core

# Final stage
FROM debian:bookworm-slim

# Install ca-certificates for HTTPS and curl for health checks
RUN apt-get update && apt-get install -y \
    ca-certificates \
    curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /opt/edg

# Copy binary from builder
COPY --from=builder /build/edg-core /opt/edg/bin/edg-core

# Copy configs and templates
COPY configs /opt/edg/configs
COPY templates /opt/edg/templates

# Create non-root user
RUN useradd -m -u 1000 edg && \
    chown -R edg:edg /opt/edg && \
    mkdir -p /opt/edg/data && \
    chown -R edg:edg /opt/edg/data

USER edg

# Expose ports
EXPOSE 4222 8222

# Health check (NATS monitoring endpoint)
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD curl -f http://localhost:8222/healthz || exit 1

# Run the application
CMD ["/opt/edg/bin/edg-core"]
