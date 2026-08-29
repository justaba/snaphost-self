# syntax=docker/dockerfile:1

# ---------------------------------------------------------------------------
# Stage 1: Build both binaries
# ---------------------------------------------------------------------------
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build

# Build context is snaphost-backend/ — the module root (Task 1 item 4).
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build API binary. Output to /out/ to avoid colliding with the source tree.
RUN CGO_ENABLED=0 go build -ldflags='-s -w' -o /out/api ./runner-svc/cmd/api

# Build Watchdog binary.
RUN CGO_ENABLED=0 go build -ldflags='-s -w' -o /out/watchdog ./runner-svc/cmd/watchdog

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
