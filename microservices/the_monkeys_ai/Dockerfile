# Build stage
FROM python:3.10-slim as builder

# Install system dependencies needed for building
RUN apt-get update && apt-get install -y \
    build-essential \
    curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy requirements first for better layer caching
COPY microservices/the_monkeys_ai/requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt

# Copy only the AI service source (keeps the image lean; the service is
# self-contained and does not import from the rest of the monorepo)
COPY microservices/the_monkeys_ai ./microservices/the_monkeys_ai

# Generate proto files during build
WORKDIR /app/microservices/the_monkeys_ai
RUN python -m grpc_tools.protoc -I. --python_out=. --grpc_python_out=. gw_recom.proto

# Production stage - using minimal Python slim
FROM python:3.10-slim

# Create non-root user for security
RUN groupadd -g 1001 appgroup && \
    useradd -u 1001 -g appgroup -s /bin/sh -m appuser

# Install only runtime dependencies
RUN apt-get update && apt-get install -y \
    curl \
    && rm -rf /var/lib/apt/lists/* \
    && apt-get clean

WORKDIR /app

# Copy Python packages from builder stage (installed to system site-packages)
COPY --from=builder /usr/local/lib/python3.10/site-packages /usr/local/lib/python3.10/site-packages
COPY --from=builder /usr/local/bin /usr/local/bin

# Copy application code and generated proto files, setting ownership in the same
# layer. A separate `RUN chown -R` would duplicate the entire tree into a new
# layer, doubling image size.
COPY --from=builder --chown=appuser:appgroup /app .

# Switch to non-root user
USER appuser

# Set environment variables
ENV PYTHONPATH=/app

# Define port arguments from environment
ARG AI_ENGINE_PORT
ARG AI_ENGINE_HEALTH_PORT

# Set port environment variables for runtime
ENV AI_ENGINE_PORT=${AI_ENGINE_PORT}
ENV AI_ENGINE_HEALTH_PORT=${AI_ENGINE_HEALTH_PORT}

# Expose ports dynamically (gRPC and health check)
EXPOSE ${AI_ENGINE_PORT} ${AI_ENGINE_HEALTH_PORT}

# Health check using the existing HTTP endpoint
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD ["curl", "-f", "http://localhost:${AI_ENGINE_HEALTH_PORT}/health"]

# Run the service
CMD ["python", "-u", "microservices/the_monkeys_ai/main.py"]