# syntax=docker/dockerfile:1.7
FROM golang:1.26.1-alpine AS builder

RUN apk add --no-cache git ca-certificates
WORKDIR /app
COPY go.mod go.sum ./
ARG GOPROXY=https://proxy.golang.org,direct
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=secret,id=ca_cert,required=false,target=/tmp/extra-ca.crt \
    if [ -f /tmp/extra-ca.crt ]; then cp /tmp/extra-ca.crt /usr/local/share/ca-certificates/extra-ca.crt && update-ca-certificates; fi; \
    GOPROXY=${GOPROXY} GOSUMDB=off go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags='-w -s' \
    -o social-post-service ./microservices/the_monkeys_social_post/main.go

FROM alpine:3.19
RUN addgroup -g 1001 -S appgroup && adduser -u 1001 -S appuser -G appgroup && \
    apk add --no-cache ca-certificates tzdata
COPY --from=builder --chown=appuser:appgroup /app/social-post-service /app/social-post-service
USER appuser
WORKDIR /app
ARG SOCIAL_POST_SERVICE_PORT=50062
EXPOSE ${SOCIAL_POST_SERVICE_PORT}
ENTRYPOINT ["./social-post-service"]
