# syntax=docker/dockerfile:1

# ---------------------------------------------------------------------------
# Stage 1: Build both binaries
# ---------------------------------------------------------------------------
FROM golang:1.25-bookworm AS builder

RUN sed -i \
        -e 's|http://deb.debian.org|http://mirror.yandex.ru|g' \
        -e 's|http://security.debian.org|http://mirror.yandex.ru|g' \
        /etc/apt/sources.list.d/debian.sources && \
    apt-get update && \
    apt-get install -y --no-install-recommends git && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

WORKDIR /build

# Build context is snaphost-backend/ — the module root. It already was, because
# this service needed the shared package; the difference since Task 1 item 4 is
# that there is one go.mod at the top instead of one per service plus a replace.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build API binary. Output to /out/ to avoid colliding with the source tree.
RUN CGO_ENABLED=0 go build -ldflags='-s -w' -o /out/api ./builder-svc/cmd/api

# Build Worker binary.
RUN CGO_ENABLED=0 go build -ldflags='-s -w' -o /out/worker ./builder-svc/cmd/worker

# ---------------------------------------------------------------------------
# Stage 2: Runtime (debian-slim — need trivy binary + ca-certificates)
# ---------------------------------------------------------------------------
FROM debian:12-slim

RUN sed -i \
        -e 's|http://deb.debian.org|http://mirror.yandex.ru|g' \
        -e 's|http://security.debian.org|http://mirror.yandex.ru|g' \
        /etc/apt/sources.list.d/debian.sources && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git && \
    # Install Trivy via official install script.
    curl -sfL https://raw.githubusercontent.com/aquasecurity/trivy/main/contrib/install.sh | sh -s -- -b /usr/local/bin && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Use the deployment contract's fixed non-root UID/GID. The worker reads a
# 0400/0600 bind-mounted service-account key owned by the VDS deployer (1000),
# so a dynamically allocated system UID would make the secret unreadable.
RUN groupadd --gid 1000 snaphost && \
    useradd --uid 1000 --gid 1000 --create-home --home-dir /home/snaphost snaphost

# Create workdir root.
RUN mkdir -p /var/snaphost/builds && chown snaphost:snaphost /var/snaphost/builds

COPY --from=builder /out/api /api
COPY --from=builder /out/worker /worker

USER 1000:1000

EXPOSE 8082

# Default command is the worker; override via `command:` in docker-compose for API.
CMD ["/worker"]
