# Build stage
FROM golang:1.26.1-alpine AS builder

RUN apk add --no-cache git ca-certificates wget

WORKDIR /app

COPY go.mod go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOMAXPROCS=1 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -o discussions-service ./microservices/the_monkeys_discussions/main.go

RUN wget -qO /tmp/grpc_health_probe https://github.com/grpc-ecosystem/grpc-health-probe/releases/download/v0.4.25/grpc_health_probe-linux-amd64 && \
    chmod +x /tmp/grpc_health_probe

FROM alpine:3.19

RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup

RUN apk add --no-cache ca-certificates && \
    update-ca-certificates

RUN mkdir -p /app/logs /app/config && \
    chown -R appuser:appgroup /app

COPY --from=builder /app/discussions-service /app/discussions-service
COPY --from=builder /tmp/grpc_health_probe /usr/local/bin/grpc_health_probe
COPY --chown=appuser:appgroup config/ /app/config/

WORKDIR /app
USER appuser

ARG DISCUSSIONS_SERVICE_PORT=50064
ENV DISCUSSIONS_SERVICE_PORT=${DISCUSSIONS_SERVICE_PORT}

HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD grpc_health_probe -addr=:${DISCUSSIONS_SERVICE_PORT} || exit 1

EXPOSE ${DISCUSSIONS_SERVICE_PORT}

ENTRYPOINT ["./discussions-service"]
