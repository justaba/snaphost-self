# Packages and external components

Status: Current
Type: Architecture
Updated: 2026-08-30

The old service names survive in a few interface names and comments, but they
are packages in one process now.

## Application packages

| Package | Responsibility |
| --- | --- |
| gateway | Gin middleware, local authentication integration, Casbin RBAC, request enrichment and WebSocket log access. |
| control | Operator auth, sessions, API keys, projects, deploys, domains, the operator audit log, SQLite schema and saga orchestration. |
| builder | Request validation, clone or archive unpack, project detection, Dockerfile acquisition, BuildKit export and Trivy scanning. |
| runtime | Docker container lifecycle, runtime hardening, liveness probing, log forwarding and expiry watchdog. |
| ai | Built-in Dockerfile templates, OpenRouter fallback, response validation, cache and usage records. |
| panel | React/Vite assets embedded into the Go binary with go:embed and served as an SPA. |
| wiring | Direct adapters that satisfy package interfaces formerly implemented by HTTP clients. |
| logbus and buildevents | In-process event fan-out and bounded log history. |
| uploads and gitcreds | File-backed upload staging and TTL-bound credentials held only in memory. |
| memlimit | Reads the cgroup limit and derives a Go memory limit with space reserved for Trivy and page cache. |
| shared | Cross-cutting webhook authentication and Dockerfile validation helpers. |

The application entry point is cmd/snaphost. cmd/control-migrate is a one-shot
binary for applying the same SQLite baseline before a production rollout.

## External components

| Component | Current role |
| --- | --- |
| SQLite | Durable application state in the snaphost data volume. It is a library inside the process, not a service. |
| Docker daemon | Image store, user-container lifecycle and networks. Access through the socket is root-equivalent on the host. |
| BuildKit | Rootless daemon used to build images. The result is streamed into the host Docker image store; no registry is involved. |
| Traefik | Local-development routing from generated hostnames to user containers through Docker labels. |
| Caddy | Chosen production edge, but the current Docker integration and installer are not complete. |
| Trivy | Child process in the application container that scans each built local image. |

## Operator panel

The panel source lives in snaphost-backend/web. The Dockerfile builds it in a
Node stage and copies the assets into internal/panel/dist before compiling the
Go binary. A clean source checkout without built assets still compiles an
API-only binary; the production image always includes the panel.

The browser uses same-origin API calls and the snaphost_session cookie.
Non-browser clients use sk_ API keys.
