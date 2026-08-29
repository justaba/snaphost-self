# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Snaphost is an instant deployment platform ("paste a repo URL and see your project live"). This repository owns the Go backend (`snaphost-backend/`), shared infra (`infra/`), and Terraform. The React frontend is maintained independently in [justaba/snaphost-ui](https://github.com/justaba/snaphost-ui); Supabase Auth configuration and identity-schema migrations are maintained independently in [justaba/snaphost-supabase](https://github.com/justaba/snaphost-supabase).

The MCP server for AI-agent clients lives in its own public repository, [justaba/snaphost-mcp](https://github.com/justaba/snaphost-mcp), published to npm as `@snaphost/mcp`. It is a pure consumer of the public API (`POST /api/v1/deploys/upload`, `POST/GET/DELETE /api/v1/deploys`, `GET /api/v1/deploys/:id/logs`) authenticated with a Task 14a `sk_` API key — so a breaking change to any of those endpoints breaks it, and it releases on its own cadence.

## Commands

All top-level operations are driven by the root `Makefile` and `docker compose` against `infra/docker-compose.yml`.

- `make dev` / `make dev-backend` — bring up the local backend services, Traefik, PostgreSQL, and Redis.
- `make dev-frontend`, `make build-frontend`, and `make lint-frontend` — convenience commands for the sibling frontend checkout at `FRONTEND_DIR` (default `../snaphost-ui`).
- `make build` — build all backend Docker images.
- `make lint` — runs `golangci-lint` in every Go module, plus a `--build-tags yandex` pass over `runner-svc`. Requires golangci-lint **v1.64.x**; config is [snaphost-backend/.golangci.yml](snaphost-backend/.golangci.yml), found by walking up from each module directory. The v2 config schema is incompatible — upgrading means changing that file and the pinned version in `pipeline.yml` together. CI builds the linter with `go install` rather than downloading a release binary: golangci-lint refuses a module whose Go version exceeds the toolchain it was built with, and the published v1.64.8 binary is built with go1.24 while most modules target 1.25.
- `make test` — runs `go test ./...` in every Go module, plus a `-tags yandex` pass over `runner-svc`. To run one test: `cd snaphost-backend/api-gateway && go test ./middleware -run TestJWT`.
- `make logs` / `make logs-svc SVC=api-gateway` — tail compose logs.
- `make clean` — `docker compose down -v --remove-orphans` (destroys volumes).
- Supabase local: from a sibling checkout use `make -C ../snaphost-supabase supabase-start` / `supabase-stop` / `supabase-status`. The standalone repository pins its CLI version and owns `supabase/config.toml` plus all identity-schema migrations. `SUPABASE_URL` in `infra/.env` must be `http://host.docker.internal:54321`, never the Kong container name: the CLI puts Kong on its own network and that wiring dies on every `supabase stop`. Nothing applies the standalone migrations automatically — see [local-development.md](docs/operations/local-development.md) for the drift audit and the admin-role procedure.

`make lint` and `make test` iterate over `GO_MODULES` in the Makefile: `api-gateway`, `user-billing`, `builder-svc`, `runner-svc`, `ai-orchestrator`, `router-svc`, `shared`. Add a new service to that variable and to the `go` matrix in [pipeline.yml](.github/workflows/pipeline.yml) — the two lists are not derived from each other, and a module missing from either is silently never checked. `make build` is `docker compose build` and does not iterate modules.

Before running anything, copy `infra/.env.example` to `infra/.env` (the backend services `env_file: .env` from compose). UI env belongs in the sibling repository's `.env.local`; never copy its production deployment key into this repository.

`router-svc` is also part of the backend tree and is covered by `make lint` / `make test` and by CI. It has no Compose service in the local dev path — local runtime routing is Traefik's job.

## Backend architecture

The Go backend is a set of microservices under `snaphost-backend/`. `api-gateway` is the only public entry point; everything else is reachable only over the internal Docker network and authenticated with a shared `X-Webhook-Secret` header (see [shared/webhook.go](snaphost-backend/shared/webhook.go), constant-time compare). All services expose `/health` and `/metrics` (Prometheus).

### Service map & deploy saga

The end-to-end deploy flow is a saga orchestrated by `user-billing`:

1. UI → `api-gateway` → `user-billing` `POST /api/v1/deploys` — reserves coins, persists a `deploy_sagas` row, returns `202`.
2. `user-billing` saga worker → `builder-svc` `POST /api/v1/build` — clones repo, optionally calls `ai-orchestrator` to generate a Dockerfile, runs Trivy, builds via BuildKit, pushes to registry. Status posted back to `user-billing` `POST /internal/deploys/:id/status`.
3. On build success → `runner-svc` `POST /internal/deploys` — runs the image (Docker / Yandex Serverless Containers / VK Cloud, selected by `RUNNER_BACKEND`), wires Traefik routing, returns the public URL.
4. `user-billing` commits the coin reservation; on any failure step the saga refunds. A separate watchdog in `runner-svc` enforces TTLs and tells `user-billing` to mark deploys expired.
5. Build/run logs are published to Redis pub/sub by builder & runner; `user-billing` exposes them over WebSocket at `/api/v1/deploys/:id/logs`.

### api-gateway

`api-gateway` is a Gin-based reverse-proxy gateway. It does not own business logic — it authenticates, rate-limits, authorizes, and proxies only to implemented downstream HTTP services defined in [config.go](snaphost-backend/api-gateway/config/config.go): `user-billing` and `ai-orchestrator`. Build orchestration is reached through `user-billing`'s deploy saga, runtime provisioning is handled by `runner-svc` through internal saga calls, and log streaming is handled by the Redis-backed log stream endpoint rather than by a separate public `log-streamer` service.

### Request lifecycle — middleware order is load-bearing

[main.go](snaphost-backend/api-gateway/main.go) registers handlers in a precise order; do not reorder without understanding why:

1. **Before any middleware:** `/health`, `/metrics`, and `POST /internal/webhooks/supabase` are registered directly on the engine so they bypass auth, rate-limiting, and Casbin.
2. **Middleware chain** (in order): `gin.Recovery` → `RequestID` → `Logger` → `RateLimit` → `JWT` → `Casbin` → `Enrich`.
3. **Routes** (under `/api/v1/*` and `/ws/*`) are all registered as `r.Any("/<group>/*path", proxy.HTTP(...))` catch-alls that forward to the mapped downstream service.

The JWT and Casbin middlewares both consult the `PublicRoutes` map in [middleware/jwt.go](snaphost-backend/api-gateway/middleware/jwt.go) to skip validation for `auth/register`, `auth/login`, `/health`, `/metrics`, and the Supabase webhook. If you add a new public endpoint, update that map — the proxy/Any routes will otherwise fail Casbin lookup.

### Auth — Supabase JWT with JWKS

JWTs are Supabase-issued RS256 tokens. [jwt.go](snaphost-backend/api-gateway/middleware/jwt.go) implements its own JWKS cache (1h TTL) that fetches from `${SUPABASE_URL}/auth/v1/.well-known/jwks.json` and is prefetched at startup (fatal if prefetch fails). The custom `SupabaseClaims` struct reads a non-standard `snaphost_role` claim to determine RBAC role; falls back to `"user"` if empty. The user ID (`sub`), email, and role are set on the Gin context.

### RBAC — Casbin with `keyMatch2`

[rbac_model.conf](snaphost-backend/api-gateway/rbac_model.conf) + [rbac_policy.csv](snaphost-backend/api-gateway/rbac_policy.csv) define roles (`guest`, `user`, `admin` — `admin` inherits `user` via `g, admin, user`). The Casbin middleware enforces against `c.Request.URL.Path` (the actual URL, with IDs substituted), not Gin's `FullPath` — the policy file uses `:id` placeholders that `keyMatch2` resolves. When adding a new proxied endpoint, add a matching policy line.

### Rate limiting — Redis sliding window

[ratelimit.go](snaphost-backend/api-gateway/middleware/ratelimit.go) runs *before* JWT, so IP-based limits always apply. Once JWT runs and sets the user context, a second RateLimit pass would apply per-user limits — but note this middleware only runs once; the per-user branch only triggers if `ContextKeyUserID` is already set when RateLimit runs. Today that won't happen (JWT runs after), so per-user and per-deploy limiting paths are effectively dead code. Be careful if you touch middleware ordering here.

### Supabase user sync webhook

[webhooks/supabase.go](snaphost-backend/api-gateway/webhooks/supabase.go) handles a Supabase Database Webhook fired on `INSERT` into `public.users`, authenticated with the `X-Webhook-Secret` header against `SUPABASE_WEBHOOK_SECRET`. It POSTs to the `user-billing` service's `/internal/users` to seed the user with `INITIAL_VIBECOIN_BALANCE` credits. Webhook routing is registered before middleware in `main.go`, not in `routes/`.

### Proxying

[proxy/http.go](snaphost-backend/api-gateway/proxy/http.go) is a thin `httputil.ReverseProxy` wrapper. Gin's `*path` catch-all is read in the handler and forwarded via a context value so the Director can build the upstream URL. Adds `X-Proxied-By: api-gateway` to responses and returns `502 bad_gateway` on upstream failure. WebSocket proxy lives in `proxy/websocket.go` and is wired for `/ws/logs/*path`.

### user-billing (port 8081)

Wallet + deploy-saga orchestrator. Owns the `wallets`, `transactions`, `deploys`, `deploy_sagas`, `projects`, and `custom_domains` Postgres tables (migrations in [user-billing/db/](snaphost-backend/user-billing/db/)). Two surfaces:

- **Public** (proxied by api-gateway under `/api/v1/`): `GET /billing`, `POST/GET/DELETE /deploys[/:id]`, `GET /deploys/:id/logs` (WebSocket — aggregates Redis pub/sub from builder/runner), `POST/GET /domains`, `DELETE /domains/:id`, `POST /domains/:id/target`, and the admin read surface below.
- **Internal** (`X-Webhook-Secret`): `POST /internal/users` (called by api-gateway's Supabase webhook to seed `INITIAL_BALANCE` coins), `POST /internal/billing/{topup,reserve,commit,refund}`, `POST /internal/deploys/:id/status`, `POST /internal/deploys/:id/running`, `GET /internal/deploys/expired`.

`POST /billing/topup` is **internal-only** and takes an explicit `user_id`. It was public until 2026-08-07, crediting whatever amount the request body asked for with no payment verification — free coins for any account or `sk_` key. When payments land, the provider's verified callback is what calls it, with the provider's payment id as `idempotency_key`. Regression tests in `routes/routes_test.go` and the gateway's `routes/rbac_policy_test.go` keep it off the public surface.

**Operator console (Task 17a).** `GET /api/v1/admin/*` in [internal/admin/](snaphost-backend/user-billing/internal/admin/) — overview, users (+ their deploys, ledger, projects, domains, keys), global deploy/transaction/domain lists, and a deploy detail carrying its saga row. Read-only: mutations wait for an audit trail. Authorised twice — the gateway's Casbin policy for the `admin` role, and a second `X-User-Role` check inside user-billing, so a missing policy line cannot expose it. The `admin` role comes from the `snaphost_role` JWT claim written by a Supabase Postgres hook that is configured by hand; without that hook everyone falls back to `user`. Migration 0012 added the `users` table (identity + email), populated by the same Supabase webhook that seeds the wallet.

The saga worker runs in-process (toggle via `SAGA_WORKER_ENABLED`, default on) and consumes a Redis Stream — if `REDIS_URL` is unset, the saga path is disabled and only synchronous wallet ops work. Stuck sagas are resumed on a `SAGA_RESUME_INTERVAL_SEC` ticker; build steps time out after `SAGA_BUILD_TIMEOUT_MIN`. Each deploy costs `DEPLOY_COST_COINS` (vibecoins).

Reserve → commit/refund is the atomicity primitive — never deduct directly from `wallets`, always go through reserve so the saga can roll back.

**Project / deploy / alias (Task 16).** A `projects` row is the permanent, owner-scoped publish target; a deploy is one immutable build addressable at its own subdomain; a `custom_domains` row is an alias pointing at exactly one deploy. Attaching a domain returns a TXT challenge at `_snaphost-verify.<domain>`; an in-process verifier flips `pending` → `verified` and re-checks verified domains periodically, so a domain that stops pointing at us loses verification. **Only a `verified` domain ever routes.** Alias-pinned deploys are exempt from TTL expiry — the expired-deploy sweep instead reclaims un-aliased TTL expiry, aliases idle past `ALIAS_IDLE_GC_DAYS`, and deploys beyond `PROJECT_DEPLOY_RETENTION` per project. Per-deploy TTL comes from `DEPLOY_TTL_MIN` and is capped by `DEPLOY_TTL_MAX_MIN` here, because runner-svc applies any non-zero `ttl_minutes` with no upper bound. Attach is refused while both `DOMAIN_CNAME_TARGET` and `DOMAIN_A_RECORD_TARGET` are empty: no stable public address has been reserved to point DNS at. TLS for foreign hostnames (task 16c) is not implemented — the wildcard certificate cannot cover user domains.

### builder-svc (port 8082)

Async build pipeline. Split into an HTTP API (`cmd/api/`) and a worker (`cmd/worker/`) — the API only enqueues and returns `202`; the worker does the work.

- `POST /api/v1/build` accepts `{deploy_id, user_id, repo_url, branch, build_args}`, validates build args (`[A-Z_][A-Z0-9_]{0,63}`, max 20), enqueues onto a Redis Streams consumer group, returns `job_id`.
- Worker: clones with go-git → if no Dockerfile present, calls `ai-orchestrator` `POST /internal/ai/generate-dockerfile` → runs Trivy scan → builds via BuildKit (`BUILDKIT_HOST`, default `tcp://buildkitd:1234`) → pushes to `REGISTRY_URL` (optional Yandex IAM auth) → posts status to `user-billing`.
- Per-user concurrency cap via `MAX_CONCURRENT_BUILDS_PER_USER`; repo size and clone time are also bounded.

There is no Postgres dependency — Redis Streams is the only persistent state, and job results are published back via webhook rather than stored locally.

### runner-svc (port 8084)

Runtime/lifecycle for user containers. Pluggable execution backend selected by `RUNNER_BACKEND` (`docker` | `yandex` | `vk`, default `docker`). Internal-only API:

- `POST /internal/deploys` — start container with `{deploy_id, user_id, image_ref, env, port, ttl_minutes}`, attaches it to the `snaphost-net` Docker network for Traefik routing on `*.${DOMAIN_SUFFIX}`, applies `CONTAINER_MEMORY_MB` / `CONTAINER_CPU_LIMIT`. **Liveness probe (Task 15b):** before reporting `running`, the backend's optional `Prober` implementation checks that something answers on the injected port (`RUNTIME_PROBE_ENABLED`, `RUNTIME_PROBE_TIMEOUT_SEC`). On failure the runtime is torn down, the deploy is marked `failed` with a reason naming `PORT`, and the endpoint answers `422 probe_failed` — which the saga treats as terminal and refunds. Any 4xx except 429 from this endpoint is terminal for the saga; 5xx is retried.
- `DELETE /internal/deploys/:id` — stop & remove.
- `GET /internal/deploys/:id/status?container_id=...` — health check.

Container stdout/stderr is streamed into Redis pub/sub channels that `user-billing`'s WebSocket endpoint subscribes to — **on the Docker backend only**. On Yandex, a user container's runtime output goes to Cloud Logging instead (`YANDEX_LOG_GROUP_ID`, Task 13a) and `StreamLogs` returns `ErrRuntimeLogsAreOperatorOnly`, because runtime output is operator-only: users get build logs plus a failure recommendation, not raw application output. A separate watchdog binary (`cmd/watchdog/`) periodically queries `user-billing`'s `/internal/deploys/expired` and tears down anything past TTL. The Yandex backend needs `YANDEX_SA_KEY_PATH` / `YANDEX_FOLDER_ID` / `YANDEX_REGISTRY_URL`.

For Yandex, `YANDEX_ROUTING_MODE` selects the routing strategy:

- `gateway`: legacy mode; `runner-svc` mutates the shared API Gateway spec per deploy.
- `router`: preferred mode; `runner-svc` creates/deletes Serverless Containers only, and the static API Gateway wildcard route sends all runtime traffic to `router-svc`.

### router-svc

Central runtime router for Yandex `YANDEX_ROUTING_MODE=router`. It is deployed as a Yandex Serverless Container behind the wildcard API Gateway route for `*.${DOMAIN_SUFFIX}`. For each incoming request, it normalizes the `Host` (a host under `${DOMAIN_SUFFIX}` must still be exactly one label deep; anything else is treated as a custom domain and looked up by full hostname), asks `user-billing` for the running deploy mapping via `GET /internal/routes?host=<host>`, gets the target Serverless Container invocation URL, signs the upstream call with a Yandex IAM token, and proxies the HTTP request to the user container.

`router-svc` also strips the `Domain` attribute from `Set-Cookie` responses of deploys served on a generated hostname ([cookies.go](snaphost-backend/router-svc/internal/router/cookies.go)), so one tenant cannot set a cookie the whole deploy suffix receives. Custom domains are exempt — that registrable domain belongs to one customer. This is a stopgap for the missing Public Suffix List entry: it cannot see cookies set by page JavaScript, and it does not apply on the local Traefik path.

This service is not part of the local Docker dev path. Local dev still uses Docker containers plus Traefik-style subdomain routing. Yandex router deployment is described in [docs/operations/yandex-runtime.md](docs/operations/yandex-runtime.md) and [docs/operations/yandex-cloud-setup.md](docs/operations/yandex-cloud-setup.md); Terraform wiring lives in [terraform/yandex/router_container.tf](terraform/yandex/router_container.tf) and [terraform/yandex/api_gateway_spec.yaml](terraform/yandex/api_gateway_spec.yaml).

### ai-orchestrator (port 8083)

LLM-backed Dockerfile generator. Single internal endpoint: `POST /internal/ai/generate-dockerfile` taking `{deploy_id, user_id, repo_contents[]}` and returning a generated Dockerfile. Talks to OpenRouter (`OPENROUTER_API_KEY`, model from `OPENROUTER_MODEL` in `vendor/model` form, e.g. `openai/gpt-4o-mini`). Per-request deadline `LLM_TIMEOUT` (default 30s).

Owns two Postgres tables (migrations in [ai-orchestrator/db/](snaphost-backend/ai-orchestrator/db/)): `ai_cache` (7-day TTL response cache keyed on repo signature — repeat requests don't re-hit the LLM) and `ai_usage` (per-user token/cost accounting). A circuit breaker opens after 10 consecutive OpenRouter failures and stays open for 60s, so a flaky vendor doesn't take builds down.

### shared

Tiny library imported by the internal-facing services. `shared.WebhookAuth(secret)` is the Gin middleware all internal endpoints use for the `X-Webhook-Secret` header. `shared/validator/` holds Dockerfile syntax validation used by builder-svc and ai-orchestrator. Add new cross-service helpers here rather than copying.

## Frontend repository

Frontend architecture, browser environment variables, legal-document sources,
CI, and deployment live in [justaba/snaphost-ui](https://github.com/justaba/snaphost-ui).
Keep it as a sibling checkout for local full-stack work. Backend API changes must
remain compatible with the frontend rollback targets documented there; record
backend and frontend release SHAs independently.

## Domains

Two registrable domains, deliberately: `snaphost.ru` serves the dashboard (apex, `www`) and the public API (`api.`), while `snaphost.pw` serves user deploys (`proj-<id>.`) and the custom-domain edge (`edge.`). `snaphost.online` served deploys until 2026-08-03 and is parked as a spare, listed in `RESERVED_DOMAINS`. Browsers scope cookies, storage, and blocklist reputation per registrable domain, so user-controlled content must not share the domain sessions live on. `user-billing` refuses to attach hostnames under `DOMAIN_SUFFIX` (ours to allocate) or under `RESERVED_DOMAINS` (the control-plane domain). Both resolve to the same host today — see [deployment-model.md](docs/architecture/deployment-model.md) and [public-address.md](docs/operations/public-address.md). `snaphost.pw` still needs a Public Suffix List entry to isolate one user's deploy from another's cookies; until it lands, `router-svc` strips wide `Set-Cookie` domains as a stopgap.

## Infra & deploy

- `infra/docker-compose.yml` is the **local development** manifest. Production uses `infra/docker-compose.prod.yml`, which consumes only GHCR images pinned to a 40-character Git SHA and contains no `build:`, Traefik, local registry, or Docker socket.
- Traefik v3 is in the local compose file with `providers.docker.exposedbydefault=false` and no service labels — it's staged for future use (routing user-deployed projects onto subdomains of `DOMAIN`) but not currently routing any traffic.
- CI ([.github/workflows/pipeline.yml](.github/workflows/pipeline.yml)) tests the backend and infra, then publishes backend images to `ghcr.io/<repo>/<service>:<sha>`. The manual production workflow deploys only those images. Frontend builds and deployments run in the standalone frontend repository and never rebuild or restart backend services.

### Production operations

Production is live on one VDS (`135.106.166.76`), serving `snaphost.ru` (dashboard/API) and `snaphost.pw` (deploys). Three shell entry points own its lifecycle, all covered by test suites that fake every external command and run in `pipeline.yml`'s `shell` job:

- [infra/deploy.sh](infra/deploy.sh) — `preflight` / `deploy <sha>` / `rollback`. Takes a PostgreSQL dump before migrations, rolls services out in dependency order, publishes api-gateway last, and refuses to roll back across a migration without `MIGRATIONS_BACKWARD_COMPATIBLE=true`. Never restores a database.
- [infra/backup.sh](infra/backup.sh) — scheduled dump on a systemd timer ([infra/systemd/](infra/systemd/)). Shares `deploy.sh`'s lock, verifies the archive's table of contents before publishing, encrypts with `age`, copies off-host to S3, and prunes on a retention policy that never touches a dump the deployment state references.
- [.github/scripts/uptime-check.sh](.github/scripts/uptime-check.sh) — external probe run every 10 minutes by [uptime.yml](.github/workflows/uptime.yml), opening one GitHub issue per incident.

When changing any of these, run `infra/tests/deploy_test.sh` and `infra/tests/backup_test.sh`. They need GNU coreutils and `flock`, so on Windows run them in a Linux container. Procedures live in [docs/operations/](docs/operations/) — [backups](docs/operations/backups.md), [monitoring](docs/operations/monitoring.md), [rollback](docs/operations/rollback.md).

## Terraform — Yandex Cloud (runner-svc backend)

[terraform/yandex/](terraform/yandex/) provisions the Yandex Cloud resources that `runner-svc` needs when run with `RUNNER_BACKEND=yandex`. It is run once per environment from a workstation using a bootstrap service-account key (`var.bootstrap_key_file`); apply outputs feed straight into the runner/builder env vars.

What it creates:

- **Container Registry** ([registry.tf](terraform/yandex/registry.tf)) — single `snaphost` registry. Output `registry_url` (`cr.yandex/<id>/snaphost`) is what builder-svc uses as `REGISTRY_URL` and runner-svc as `YANDEX_REGISTRY_URL` to pull images.
- **Service accounts** ([service_accounts.tf](terraform/yandex/service_accounts.tf)) — least-privilege split:
  - `snaphost-runner`: `serverless-containers.editor` + `serverless.containers.invoker` + `api-gateway.editor` + `container-registry.images.puller` + `logging.reader`. Authorized key written to `terraform/yandex/.keys/runner.json` — this is what `YANDEX_SA_KEY_PATH` points at on the runner host.
  - `snaphost-builder`: only `container-registry.images.pusher`. Key at `.keys/builder.json` for builder-svc registry pushes.
- **DNS + wildcard cert** ([dns.tf](terraform/yandex/dns.tf), [certificate.tf](terraform/yandex/certificate.tf)) — public DNS zone for `var.domain_name`, plus a Yandex CM wildcard certificate for `*.<domain>` validated via DNS records in the same zone. Output `dns_ns_servers` must be set at the registrar for delegation to work.
- **API Gateway** ([api_gateway.tf](terraform/yandex/api_gateway.tf) + [api_gateway_spec.yaml](terraform/yandex/api_gateway_spec.yaml)) — single `snaphost-router` gateway bound to `*.<domain>` with the wildcard cert. In preferred central-router mode, the OpenAPI spec has one wildcard proxy route to the Terraform-managed `router-svc` Serverless Container. In legacy `YANDEX_ROUTING_MODE=gateway`, runner-svc can still mutate the spec per deploy. Output `api_gateway_id` is passed to runner-svc as `YANDEX_API_GATEWAY_ID`.

Wiring back to runner-svc env (matches the Yandex backend section above):

| Terraform output | runner-svc env var |
| --- | --- |
| `runner_key_path` (`.keys/runner.json`) | `YANDEX_SA_KEY_PATH` |
| `folder_id` | `YANDEX_FOLDER_ID` |
| `registry_url` | `YANDEX_REGISTRY_URL` |
| `runner_sa_id` | `YANDEX_RUNNER_SA_ID` |
| `api_gateway_id` | `YANDEX_API_GATEWAY_ID` |

Notes:

- `terraform.tfstate` is currently committed alongside the configs — treat the directory as sensitive and avoid editing state by hand. The `.keys/*.json` files contain private keys; never check them in.
- The Docker / VK runner backends do not use any of this — Terraform is only relevant when `RUNNER_BACKEND=yandex`.
- Routing model differs from local dev: locally, Traefik routes `*.${DOMAIN_SUFFIX}` over the `snaphost-net` Docker network; in Yandex, the API Gateway plays Traefik's role and runner-svc edits its spec instead of attaching containers to a network.
