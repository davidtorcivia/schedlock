# Build stage
FROM golang:1.22-alpine AS builder

# Install build dependencies
RUN apk add --no-cache gcc musl-dev

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o /schedlock ./cmd/server

# Runtime stage
FROM alpine:3.19

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN adduser -D -g '' schedlock

WORKDIR /app

# Copy binary from builder
COPY --from=builder /schedlock /app/schedlock

# Copy templates and static files
COPY web/templates /app/web/templates
COPY web/static /app/web/static

# Create data directory
RUN mkdir -p /app/data && chown -R schedlock:schedlock /app

# Switch to non-root user
USER schedlock

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/api/health || exit 1

# Environment variables
ENV SCHEDLOCK_DATA_DIR=/app/data \
    SCHEDLOCK_TEMPLATES_DIR=/app/web/templates \
    SCHEDLOCK_STATIC_DIR=/app/web/static

# Run the application
ENTRYPOINT ["/app/schedlock"]
