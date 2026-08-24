# Build stage
FROM golang:1.26.1-alpine AS builder

RUN apk add --no-cache git ca-certificates wget

WORKDIR /app

# Copy go mod files first (for better caching)
COPY go.mod go.sum ./

# Download dependencies with a persistent cache mount so modules
# are shared across all service builds and survive layer invalidation.
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# Cache mounts persist the Go build cache across builds — shared
# packages (gRPC, protobuf, zap, etc.) are compiled once and reused
# by all service Dockerfiles.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOMAXPROCS=4 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -o events-service ./microservices/the_monkeys_events/main.go

RUN wget -qO /tmp/grpc_health_probe https://github.com/grpc-ecosystem/grpc-health-probe/releases/download/v0.4.25/grpc_health_probe-linux-amd64 && \
    chmod +x /tmp/grpc_health_probe

# Production stage - using minimal alpine
FROM alpine:3.19

RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup

# tzdata is required: events store an organizer timezone alongside UTC times.
RUN apk add --no-cache ca-certificates tzdata && \
    update-ca-certificates

RUN mkdir -p /app/logs /app/config && \
    chown -R appuser:appgroup /app

COPY --from=builder /app/events-service /app/events-service
COPY --from=builder /tmp/grpc_health_probe /usr/local/bin/grpc_health_probe
COPY --chown=appuser:appgroup config/ /app/config/

WORKDIR /app
USER appuser

ARG EVENTS_SERVICE_PORT=50059
ENV EVENTS_SERVICE_PORT=${EVENTS_SERVICE_PORT}

HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD grpc_health_probe -addr=:${EVENTS_SERVICE_PORT} || exit 1

EXPOSE ${EVENTS_SERVICE_PORT}

ENTRYPOINT ["./events-service"]
