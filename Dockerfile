# syntax=docker/dockerfile:1

# ---- Build ------------------------------------------------------------------
FROM golang:1.22-alpine AS builder

WORKDIR /src

# Dependencies are resolved in their own layer so source edits do not re-download
# the module cache.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO is off: the SQLite driver is pure Go, so this produces a static binary
# with no C toolchain here and no shared libraries at runtime.
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X github.com/dtorcivia/schedlock/internal/server.Version=${VERSION}" \
    -o /out/schedlock ./cmd/server

# ---- Runtime ----------------------------------------------------------------
FROM alpine:3.19

# ca-certificates is needed to reach Google and the notification providers.
# The timezone database is compiled into the binary, so tzdata is not installed.
RUN apk add --no-cache ca-certificates wget && \
    adduser -D -H -u 10001 schedlock

# Templates and static assets are embedded in the binary, so the image holds
# only the executable and its data directory.
COPY --from=builder /out/schedlock /usr/local/bin/schedlock

RUN mkdir -p /data && chown schedlock:schedlock /data
VOLUME ["/data"]

USER schedlock:schedlock
WORKDIR /data

EXPOSE 8080

ENV SCHEDLOCK_DATA_DIR=/data

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8080/health || exit 1

ENTRYPOINT ["/usr/local/bin/schedlock"]
