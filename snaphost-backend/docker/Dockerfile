# syntax=docker/dockerfile:1
#
# One image, because there is one process (Task 1). It replaced five: the
# gateway, the control plane, the builder API and worker, the runtime API and
# watchdog, and the Dockerfile generator.
#
# The runtime stage is debian-slim rather than distroless. Four of the five
# images could have been distroless; this one cannot, because the build
# pipeline shells out to git for clones and to trivy for scans. Merging the
# processes means the whole binary inherits the fattest base of the set —
# a real cost of the collapse, and the reason `git` and `trivy` are the only
# things installed here.
#
# Build context is snaphost-backend/, the module root.

# ---------------------------------------------------------------------------
# Stage 1: build
# ---------------------------------------------------------------------------
FROM golang:1.25-bookworm AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -ldflags='-s -w' -o /out/snaphost ./cmd/snaphost
# The migrator stays a separate binary: a deployment may need to apply a
# schema change as its own step, before the service that would otherwise do it
# at startup is allowed to start.
RUN CGO_ENABLED=0 go build -ldflags='-s -w' -o /out/control-migrate ./cmd/control-migrate

# ---------------------------------------------------------------------------
# Stage 2: runtime
# ---------------------------------------------------------------------------
FROM debian:12-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git && \
    curl -sfL https://raw.githubusercontent.com/aquasecurity/trivy/main/contrib/install.sh | sh -s -- -b /usr/local/bin && \
    apt-get purge -y curl && apt-get autoremove -y && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Fixed non-root identity. The build workdir and any bind-mounted file have to
# be readable by exactly this uid, so a dynamically allocated system uid would
# make them unreadable — the failure that stopped a staging rollout upstream.
RUN groupadd --gid 1000 snaphost && \
    useradd --uid 1000 --gid 1000 --create-home --home-dir /home/snaphost snaphost

# Both directories are created here, with the right owner, because Docker
# seeds a fresh named volume from the image — a mount point it creates itself
# is owned by root, and this container is not.
RUN mkdir -p /var/snaphost/builds /var/snaphost/data \n && chown -R snaphost:snaphost /var/snaphost

COPY --from=builder /out/snaphost /snaphost
COPY --from=builder /out/control-migrate /control-migrate
COPY internal/gateway/rbac_model.conf /etc/snaphost/rbac_model.conf
COPY internal/gateway/rbac_policy.csv /etc/snaphost/rbac_policy.csv

ENV RBAC_MODEL_PATH=/etc/snaphost/rbac_model.conf \
    RBAC_POLICY_PATH=/etc/snaphost/rbac_policy.csv

EXPOSE 8080
USER snaphost:snaphost

ENTRYPOINT ["/snaphost"]
