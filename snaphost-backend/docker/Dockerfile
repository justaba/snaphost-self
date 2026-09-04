# syntax=docker/dockerfile:1
#
# One image, because there is one process (Task 1). It replaced five: the
# application API, build pipeline, container runtime and Dockerfile generator.
#
# The runtime stage is debian-slim rather than distroless because the build
# pipeline shells out to git for clones and operators need sqlite3 for safe
# online backups of the embedded database.
#
# Build context is snaphost-backend/, the module root.

# ---------------------------------------------------------------------------
# Stage 1: panel
# ---------------------------------------------------------------------------
#
# The operator UI is compiled into the Go binary, so it has to exist before
# the Go stage runs. Building it here rather than trusting a directory on the
# host is the whole point: an image must not ship whatever happened to be
# lying in internal/panel/dist on somebody's workstation.
#
# Dependencies are installed from the lockfile in their own layer, before the
# sources are copied, so editing a component does not reinstall node_modules.
FROM node:22-bookworm-slim AS panel

WORKDIR /panel

RUN corepack enable

COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

COPY web/ ./

# vite.config.ts writes to ../internal/panel/dist, which is where the Go
# stage expects it — the path is the contract between the two, and it is set
# there rather than here so that a local build and an image build agree.
RUN pnpm build

# ---------------------------------------------------------------------------
# Stage 2: build
# ---------------------------------------------------------------------------
FROM golang:1.25-bookworm AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# After the sources, so it is not overwritten by the .gitkeep the repository
# carries to make //go:embed compile in a fresh checkout.
COPY --from=panel /internal/panel/dist ./internal/panel/dist

RUN CGO_ENABLED=0 go build -ldflags='-s -w' -o /out/snaphost ./cmd/snaphost
# The migrator stays a separate binary: a deployment may need to apply a
# schema change as its own step, before the service that would otherwise do it
# at startup is allowed to start.
RUN CGO_ENABLED=0 go build -ldflags='-s -w' -o /out/control-migrate ./cmd/control-migrate

# ---------------------------------------------------------------------------
# Stage 3: runtime
# ---------------------------------------------------------------------------
FROM debian:12-slim

# sqlite3 is here for one reason: taking a backup. The store is a single file
# and the tempting move is to copy it, which is wrong while a process has it
# open — under WAL the committed state is spread across the database and its
# -wal, and a plain copy of the three files is a torn snapshot that restores as
# a corrupt or stale database with nothing reporting an error. `sqlite3 .dump`
# reads it in one transaction. Curl is the production healthcheck client; the
# deploy script waits for that healthcheck instead of starting a second copy of
# this image and assuming a command exists in it.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        sqlite3 && \
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
RUN mkdir -p /var/snaphost/builds /var/snaphost/data && \
    chown -R snaphost:snaphost /var/snaphost

COPY --from=builder /out/snaphost /snaphost
COPY --from=builder /out/control-migrate /control-migrate
COPY internal/httpapi/rbac_model.conf /etc/snaphost/rbac_model.conf
COPY internal/httpapi/rbac_policy.csv /etc/snaphost/rbac_policy.csv

ENV RBAC_MODEL_PATH=/etc/snaphost/rbac_model.conf \
    RBAC_POLICY_PATH=/etc/snaphost/rbac_policy.csv

EXPOSE 8080
USER snaphost:snaphost

ENTRYPOINT ["/snaphost"]
