# syntax=docker/dockerfile:1

# ---------------------------------------------------------------------------
# Stage 1: Build both binaries
# ---------------------------------------------------------------------------
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build
ARG GO_BUILD_TAGS=""

COPY shared/ ../shared/
COPY runner-svc/go.mod runner-svc/go.sum ./
RUN go mod download

COPY runner-svc/ ./

# Build API binary. Output to /out/ to avoid colliding with the ./api source directory.
RUN CGO_ENABLED=0 go build ${GO_BUILD_TAGS:+-tags "$GO_BUILD_TAGS"} -ldflags='-s -w' -o /out/api ./cmd/api

# Build Watchdog binary.
RUN CGO_ENABLED=0 go build ${GO_BUILD_TAGS:+-tags "$GO_BUILD_TAGS"} -ldflags='-s -w' -o /out/watchdog ./cmd/watchdog

# ---------------------------------------------------------------------------
# Stage 2: Runtime (debian-slim — need ca-certificates for registry TLS)
# ---------------------------------------------------------------------------
FROM debian:12-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# NOTE: runner-svc runs as root because it needs direct access to the Docker
# daemon socket (/var/run/docker.sock) to create/stop user containers.
# Security is enforced at the user-container level (dropped caps, read-only
# rootfs, PID limits, isolated networks) — not at the runner process level.

COPY --from=builder /out/api /api
COPY --from=builder /out/watchdog /watchdog

EXPOSE 8084

# Use CMD (not ENTRYPOINT) so docker-compose command: can override cleanly.
CMD ["/api"]
