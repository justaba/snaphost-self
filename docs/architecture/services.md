# Services and responsibilities

Status: Current
Type: Architecture
Updated: 2026-07-03

| Service | Responsibility |
| --- | --- |
| `api-gateway` | Public API entry point, JWT validation, RBAC, rate limiting, proxying, and WebSocket log access. |
| `user-billing` | Wallets, deploy records, saga orchestration, compensation, route lookup, and database migrations. |
| `builder-api` | Validates build requests and writes jobs to Redis Streams. |
| `builder-worker` | Clones repositories, detects projects, obtains Dockerfiles, builds images, scans them, and publishes build events. |
| `runner-api` | Validates image/deploy ownership and creates or deletes runtime resources. |
| `runner-watchdog` | Finds expired deploys and performs TTL cleanup through the selected backend. |
| `ai-orchestrator` | Detects project shape, renders known templates, and uses an LLM when templates are insufficient. |
| `router-svc` | Routes Yandex wildcard-domain traffic to the correct running Serverless Container. |
| PostgreSQL | Durable billing, deploy, saga, transaction, and AI-cache state. |
| Redis | Build queue, build events, and deploy lifecycle logs. |
| BuildKit | Builds user container images. |

`router-svc` is part of the Yandex production path, not the local Docker path.
Local Docker runtime routing is provided by Traefik labels.
