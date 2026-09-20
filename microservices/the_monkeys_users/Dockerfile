# Build stage
FROM golang:1.26.1-alpine AS builder

# Install necessary packages for building
RUN apk add --no-cache git ca-certificates wget

WORKDIR /app

# Copy go mod files first (for better caching)
COPY go.mod go.sum ./

# Download dependencies with a persistent cache mount so modules
# are shared across all service builds and survive layer invalidation.
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy source code
COPY . .

# Build the binary with optimizations for production.
# Cache mounts persist the Go build cache across builds — shared
# packages (gRPC, protobuf, zap, etc.) are compiled once and reused
# by all service Dockerfiles.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOMAXPROCS=1 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -o user-service ./microservices/the_monkeys_users/main.go

# Download grpc_health_probe during build
RUN wget -qO /tmp/grpc_health_probe https://github.com/grpc-ecosystem/grpc-health-probe/releases/download/v0.4.25/grpc_health_probe-linux-amd64 && \
    chmod +x /tmp/grpc_health_probe

# Production stage - using minimal alpine
FROM alpine:3.19

# Create non-root user for security
RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates tzdata && \
    rm -rf /var/cache/apk/*

WORKDIR /app

# Copy the binary and health probe from builder stage
COPY --from=builder /app/user-service .
COPY --from=builder /tmp/grpc_health_probe /usr/local/bin/grpc_health_probe

# Change ownership to non-root user
RUN chown -R appuser:appgroup /app && \
    chmod +x /app/user-service

# Switch to non-root user
USER appuser

# Define port argument from environment
ARG USER_SERVICE_PORT

# Set port environment variable for runtime
ENV USER_SERVICE_PORT=${USER_SERVICE_PORT}

# Expose port dynamically
EXPOSE ${USER_SERVICE_PORT}

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/grpc_health_probe", "-addr=localhost:${USER_SERVICE_PORT}"]

# Run the binary
CMD ["./user-service"]