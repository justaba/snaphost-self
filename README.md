# snaphost
Instant deployment platform — paste a repo URL and see your project live.

## Documentation
[docs/](docs/) is the entry point: architecture, operations runbooks, decision
records, and current task status. This file covers local setup only.

## Requirements
- Docker 24+
- Docker Compose v2
- Go 1.22+
- Node.js 20+
- pnpm

## Quick start
1. Clone this repository, `justaba/snaphost-ui`, and
   `justaba/snaphost-supabase` as sibling directories.
2. Start local Auth with `make -C ../snaphost-supabase supabase-start`.
3. Copy `infra/.env.example` to `infra/.env` and fill in the required values.
4. Put the public browser variables in `../snaphost-ui/.env.local`.
5. Run `make dev-backend`, then `make dev-frontend` in another shell.
6. Open http://localhost:5173 in your browser.

## Project structure
```
snaphost/
├── .github/
│   └── workflows/
├── infra/
│   ├── .env.example
│   └── docker-compose.yml
├── snaphost-backend/
│   ├── api-gateway/       public entry point: JWT/API-key auth, RBAC, proxying
│   ├── user-billing/      wallets, deploys, saga orchestration, custom domains
│   ├── builder-svc/       clone/unpack → Dockerfile → BuildKit → scan → push
│   ├── runner-svc/        starts user containers on Docker or Yandex
│   ├── router-svc/        routes runtime traffic to the right container
│   ├── ai-orchestrator/   generates a Dockerfile when the repo has none
│   └── shared/            webhook auth and Dockerfile validation
├── terraform/yandex/      cloud resources for the Yandex runtime
└── docs/                  architecture, operations, decisions, tasks
```

The React frontend lives in the separate
[justaba/snaphost-ui](https://github.com/justaba/snaphost-ui) repository. For
the convenience targets below, keep both checkouts beside each other or set
`FRONTEND_DIR` explicitly.

Supabase Auth configuration, identity-schema migrations, and self-hosting
operations live in the separate private
[justaba/snaphost-supabase](https://github.com/justaba/snaphost-supabase)
repository. Keep it as `../snaphost-supabase` for the documented local commands.

The MCP server for AI agents lives in its own public repository,
[justaba/snaphost-mcp](https://github.com/justaba/snaphost-mcp).

## Available make commands
| Command | Description |
|---------|-------------|
| `dev` | Starts the local backend stack via docker compose |
| `dev-frontend` | Starts only the frontend dev server |
| `dev-backend` | Starts only backend services via docker compose |
| `build` | Builds all Docker images via docker compose build |
| `build-frontend` | Builds frontend production bundle |
| `lint` | Runs golangci-lint for each backend service |
| `lint-frontend` | Runs ESLint in the sibling frontend checkout |
| `test` | Runs go test ./... for each backend service |
| `stop` | Stops all docker compose services |
| `clean` | Stops and removes all containers, volumes, networks |
| `logs` | Tails logs from all docker compose services |
| `logs-svc` | Tails logs for a specific service: `make logs-svc SVC=api-gateway` |

## Services and ports
| Service | Port | Description |
|---------|------|-------------|
| snaphost-ui (sibling repository) | 5173 | Frontend React application |
| api-gateway | 8080 | The only public entry point |
| user-billing | 8081 | Wallets, deploys, saga, domains |
| builder-svc | 8082 | Build API and worker |
| ai-orchestrator | 8083 | Dockerfile generation |
| runner-svc | 8084 | Container lifecycle |
| router-svc | 8085 | Runtime routing by `Host` |
| traefik dashboard | 8090 | Local reverse proxy dashboard |
| postgres | 5432 | Primary database |
| redis | 6379 | Queue, build events, deploy logs |

Only `api-gateway` is reachable from outside; everything else answers on the
internal Docker network and authenticates with a shared `X-Webhook-Secret`.

## Environment variables
Copy `infra/.env.example` to `infra/.env` and set appropriate values.

| Variable | Description |
|----------|-------------|
| `POSTGRES_DB` | Name of the PostgreSQL database |
| `POSTGRES_USER` | PostgreSQL user |
| `POSTGRES_PASSWORD` | PostgreSQL password |
| `POSTGRES_HOST` | PostgreSQL host |
| `POSTGRES_PORT` | PostgreSQL port |
| `REDIS_URL` | Redis connection URL |
| `API_GATEWAY_PORT` | Port for the API Gateway |
| `SUPABASE_URL` | Supabase project; api-gateway fetches its JWKS from here to verify RS256 tokens. There is no shared JWT secret — the keys are Supabase's and rotate on their side |
| `SUPABASE_WEBHOOK_SECRET` | Our own shared secret for the Supabase user-seed webhook, unrelated to Supabase's keys |
| `WEBHOOK_SECRET` | Shared secret for internal service-to-service calls |
| `DOMAIN_SUFFIX` | Domain user deploys answer under |

The frontend needs its own `../snaphost-ui/.env.local` with `VITE_SUPABASE_URL`,
`VITE_SUPABASE_ANON_KEY`, and `VITE_API_URL`; these are compiled into the
bundle at build time, so they cannot be corrected by an env change later.

The full production variable set, with comments, is
[`infra/.env.production.example`](infra/.env.production.example).

## CI/CD
Pushing to `main` runs tests, linters, Terraform validation, and the
deployment-script suites, then publishes every backend image to GHCR tagged
with the commit SHA. **It does not deploy.**

Production is a manual `workflow_dispatch` against an exact SHA that must
already exist in GHCR and be an ancestor of `origin/main`. It deploys backend
containers only. Frontend CI and releases run independently in
[justaba/snaphost-ui](https://github.com/justaba/snaphost-ui), using their own
commit SHA, GitHub Environment, SSH key, and atomic release symlink.

See [production deployment](docs/operations/production-deployment.md) and
[rollback](docs/operations/rollback.md).

## Contributing
Use standard branch naming conventions (`feat/`, `fix/`, `chore/`). Make sure to run `make lint` and `make test` before opening a PR.
