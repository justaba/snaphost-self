# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

`snaphost-self` is a self-hosted deployment platform for **one operator**: point
it at a Git repository or upload a folder, get a running site on a subdomain.
It was forked on 2026-08-29 from [SnapHost](https://github.com/justaba/snaphost),
a multi-tenant SaaS, and most of the work since has been deletion — see
[completed Task 1](docs/tasks/completed/0001-collapse-to-one-binary.md).

The target is the cheapest VPS tier a provider sells, so the panel's memory
budget is the point of the design rather than a nice-to-have. The last
comparable idle measurement was **44.7 MiB across four containers before the
registry was removed**, of which the application itself was 6.8 MiB. The
current local manifest has three containers and has not been remeasured as a
complete stack.

Everything is in this repository. There is no sibling frontend checkout, no
Supabase project, no Terraform, and no cloud account: the panel is compiled into
the binary, identity is issued here, and the only runtime is the Docker daemon
on the same host.

## Shape

One Go module (`snaphost`, Go 1.25) under `snaphost-backend/`, producing one
runtime binary plus a one-shot migration command:

| Path | What it is |
| --- | --- |
| `cmd/snaphost` | The whole platform: API, panel, build pipeline, runtime, watchdog, saga worker. |
| `cmd/control-migrate` | Applies the schema. Separate so a deployment can migrate as its own step. |

`internal/` holds what used to be seven services plus the glue that replaced the
network between them:

| Package | Responsibility |
| --- | --- |
| `gateway` | Middleware chain, RBAC, WebSocket log endpoint. No business logic. |
| `control` | Sessions, API keys, deploys, projects, custom domains, the saga, admin read surface. Owns the schema. |
| `builder` | Clone or unpack, detect, generate a Dockerfile, scan, build. |
| `runtime` | Starts and stops user containers; the TTL watchdog. |
| `ai` | Dockerfile templates first, an LLM when none match. |
| `panel` | The operator UI, embedded with `go:embed`. |
| `shared` | The webhook-secret middleware and Dockerfile validation. |
| `wiring` | Adapters between packages that used to be HTTP clients of each other. |
| `logbus`, `buildevents` | In-process fan-out that replaced Redis pub/sub and streams. |
| `uploads`, `gitcreds` | File-backed upload staging; in-memory credentials with a TTL. |
| `memlimit` | Reads the cgroup limit and derives `GOMEMLIMIT` from it. |

`wiring` exists because the merge kept the package boundaries: `builder` still
calls an `AIClient` interface, `runtime` still calls a `BillingClient`. The
adapters make those direct calls. Keep new cross-package calls going through it
rather than importing another package's internals.

## Commands

Everything is driven by the root `Makefile` and `docker compose` against
`infra/docker-compose.yml`.

- `make dev` — bring up the local stack (app, BuildKit, Traefik).
- `make dev-backend` — the app and BuildKit only.
- `make dev-panel` — the panel's Vite dev server, proxying `/api` and `/ws` to
  `127.0.0.1:8080`. Requires `make dev` running.
- `make build` — build the image. The panel is built inside it, so this needs no
  local Node.
- `make build-panel` — build the panel into `internal/panel/dist`, which is where
  `go:embed` reads it. Only needed to get a panel into a binary built with plain
  `go build`.
- `make test` / `make test-panel` — `go test ./...` and `vitest run`.
- `make lint` / `make lint-panel`.
- `make logs` / `make logs-svc SVC=snaphost`.
- `make clean` — `docker compose down -v --remove-orphans`, which destroys the
  database volume.

Before the first run, copy `infra/.env.example` to `infra/.env`.

**The Compose project is named in the compose file** (`name: snaphost-self`), not
passed on the command line, so every invocation agrees on it. If you run
`docker compose` by hand from `infra/`, pass `-p snaphost-self`: the original
`snaphost` checkout sits next door with its compose in `infra/` too, and without
a project name both take the directory name and share volumes.

### Verification before a commit

```
cd snaphost-backend && go build ./... && go vet ./... && go test ./...
gofmt -l cmd internal          # must print nothing
```

`golangci-lint` runs **only in a container**:

```
MSYS_NO_PATHCONV=1 docker run --rm -v "d:/snaphost-self/snaphost-backend:/app" -w /app \
  -e GOTOOLCHAIN=auto -e GOFLAGS=-buildvcs=false golang:1.25 \
  sh -c 'go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8 >/dev/null 2>&1; $(go env GOPATH)/bin/golangci-lint run ./...'
```

It has to be `go install`, not the published binary: golangci-lint refuses a
module whose Go version exceeds the toolchain it was built with, and the v1.64.8
release is built with go1.24 while this module targets 1.25. Config is
[.golangci.yml](snaphost-backend/.golangci.yml); the v2 schema is incompatible,
so upgrading means changing that file and the pinned version together. The
linter checks goimports grouping, which `gofmt` does not.

**Static checks are not enough.** Twice in this repository they were green while
the platform was broken in a way a single run would have shown in under a
minute — a config still requiring `REDIS_URL` after Redis was removed, and a
`rollback_to` naming seven deleted services. Run the stack and exercise the
change.

Two local traps: a host proxy intercepts `localhost:8080` and answers `502`, so
use `curl --noproxy '*' http://127.0.0.1:8080/...`; and on Windows `docker` with
a volume mount needs `MSYS_NO_PATHCONV=1` in front of it.

## SQLite — three rules, each of which fails silently

The store is one SQLite file (`modernc.org/sqlite`, pure Go, `CGO_ENABLED=0`,
WAL). The schema is a single migration, `0001_baseline`, in
[internal/control/db/migrations/](snaphost-backend/internal/control/db/migrations/).
**Nobody has this installed, so a new column goes into the baseline**, not into a
migration on top of it.

1. **Timestamps are TEXT, RFC 3339, always UTC.** Compute every comparison in Go
   with `controldb.Now()` / `controldb.FormatTime()`. **Never write
   `datetime('now')` in SQL**: it produces `YYYY-MM-DD HH:MM:SS`, and the space
   where the `T` belongs sorts before every digit, so the comparison silently
   matches every row. Column DEFAULTs use `strftime('%Y-%m-%dT%H:%M:%fZ','now')`.
2. **Placeholders are positional.** One `?` per *use* of a value. PostgreSQL
   could reuse `$1`; SQLite cannot. Getting this wrong is an error at execution
   time only — the reclaim sweep never ran once because of it, while every test
   in the package inspected the SQL as a string and passed.
3. **`time.Time` does not scan; `uuid.UUID` does.** Use `controldb.Into()` and
   `controldb.IntoNull()`.

Tests that only match SQL text cannot see any of these. Execute statements
against a real migrated database — `controldb.Open` plus `controldb.RunMigrations`
in a `t.TempDir()` is three lines.

Tables: `users`, `sessions`, `projects`, `deploys`, `deploy_sagas`,
`custom_domains`, `api_keys`, `ai_dockerfile_cache`, `ai_usage_log`.

## Request lifecycle — the order is load-bearing

[cmd/snaphost/main.go](snaphost-backend/cmd/snaphost/main.go) registers handlers
in a precise order:

1. **Before any middleware:** `GET /health`, `GET /metrics`, and
   `GET /ws/logs/:id`. Health must answer when authentication is broken, and a
   browser cannot put a header on a WebSocket handshake, so that endpoint
   authenticates itself.
2. **Before user authentication, also on the engine:** `RegisterInternal` mounts
   `/internal/*` behind `shared.WebhookAuth`. Those callers present a shared
   secret, not a session; running them through `Auth` would reject every one.
3. **Middleware chain:** `Recovery` → `CORS` → `RequestID` → `Logger` →
   **`panel`** → `Auth` → `Casbin` → `Enrich` → upload body limit.
4. **Routes:** `/api/v1/*` registered directly — no proxy, no second process.

The panel sits **before `Auth` deliberately**. The obvious shape is a `NoRoute`
fallback, and it is wrong: Gin runs the global middleware for `NoRoute` too, so
the panel would be behind `Auth` and `Casbin` and the login page would answer
`401` to exactly the people who need it. The middleware claims `GET` and `HEAD`
for paths outside `/api`, `/ws`, `/internal`, `/health` and `/metrics`, and
nothing else — a mistyped API path still gets a JSON `404` rather than a page of
HTML.

`Enrich` writes `X-User-ID`, `X-User-Email` and `X-User-Role` for the handlers,
and **deletes them from the incoming request first**. That deletion is the
anti-spoofing measure; do not remove it.

## Auth — issued here

There is no external identity provider. On first start, `auth.Bootstrap` creates
the operator account and **prints a generated password once** to the container
log. It is not a default: a shipped credential is one every install shares and
most never change, and this one authorises a panel that runs containers on the
host. Losing it means resetting it, not reading it back — the bootstrap re-runs
when no account can log in with a password at all.

- **Passwords:** argon2id, PHC-encoded, OWASP's `m=7168,t=5,p=1`. The usual
  `m=19456,t=2` is equivalent in strength and left 29 MiB resident that did not
  come back; two hashes run concurrently at most, and `debug.FreeOSMemory()`
  runs after each.
- **Sessions:** opaque tokens, stored SHA-256-hashed in `sessions`, handed out
  as a `snaphost_session` cookie (HttpOnly, SameSite=Lax). The cookie name lives
  in `internal/shared` because control writes it and gateway reads it.
- **API keys:** `sk_` bearer tokens, a separate path for non-browser clients.
  `middleware.Auth` accepts exactly two credentials and a malformed
  `Authorization` header does **not** fall through to the cookie.
- **Login rate limit:** ten failures per address per five minutes, in memory.
  Only failures count; a success clears the counter.

`PublicRoutes` in [middleware/auth.go](snaphost-backend/internal/gateway/middleware/auth.go)
is `POST /api/v1/auth/login`, `/health`, `/metrics` — that is all. Both `Auth`
and `Casbin` consult it.

## RBAC — Casbin with `keyMatch2`

[rbac_model.conf](snaphost-backend/internal/gateway/rbac_model.conf) +
[rbac_policy.csv](snaphost-backend/internal/gateway/rbac_policy.csv). Two roles:
`user` and `admin`, with `g, admin, user`. The role comes from the `users.role`
column, not a token claim.

Enforcement is against `c.Request.URL.Path` — the real URL with IDs substituted
— not Gin's `FullPath`; the policy file uses `:id` placeholders that `keyMatch2`
resolves. **Adding a route means adding a policy line**, or it is refused for
every role.

## The deploy saga

Orchestrated in `internal/control/saga`, in-process, over a buffered channel.
`deploy_sagas.current_step` is the durable state: `pending` → `building` →
`built` → `provisioning` → `running`, with `compensating` / `compensated` /
`failed` on the way out.

1. `POST /api/v1/deploys` resolves or creates the project, writes the deploy and
   saga rows, enqueues, returns `202`.
2. The worker enqueues a build. The builder clones (or unpacks an upload),
   detects the project, gets a Dockerfile — template first, LLM only if none
   matches — builds with BuildKit, loads the image into Docker, and runs Trivy
   against that local image.
3. On success the runtime starts the container, attaches it to `snaphost-net`,
   waits for the liveness probe, and atomically stores the running deploy plus
   saga runtime handle before reporting the URL. A failed final write stops the
   uncommitted container and leaves the deploy retryable.
4. Failure at any step compensates and marks the deploy failed with a reason.

Interrupted sagas are rewound at startup: `RewindInterruptedBuilds` resets rows
at `building` with no image back to `pending`, because the in-process queue does
not survive a restart the way a Redis stream did. Stuck sagas are also resumed on
a `SAGA_RESUME_INTERVAL_SEC` ticker.

Billing is gone — no wallets, no reservations, no coins. One operator does not
bill themselves.

## Builder

`internal/builder`. Clones with go-git under host, size and time limits, or
unpacks an uploaded archive streamed to a file (`internal/uploads`, never held
in RAM).

**There is no registry.** BuildKit exports with `ExporterDocker` through an
`io.Pipe` into `ImageLoad`, so a built image lands directly in the daemon that
will run it, and the runtime checks it is there with `ImageInspectWithRaw`
instead of pulling. Push and pull only ever existed to reach a cloud runtime.
Images are named `snaphost/proj-<hash8>:<deploy-id>` — the account id is hashed
because the name appears in `docker ps` and in build logs.

Trivy scans the local image (`--image-src docker`). `SCAN_FAIL_ON_CRITICAL`
gates whether a finding fails the build; on failure with the gate on, the image
is removed from the daemon.

**Nothing removes a built image when its deploy is deleted or expires.** With no
registry, the disk that fills is the one the platform runs on.

## AI — templates first

`internal/ai`. `FindMatch` walks a template library in order and the first match
wins; the LLM is the fallback for what nothing covers. Two things about that
order have bitten:

- A matcher must read **both** `dependencies` and `devDependencies`. Where a
  package sits is taste for a build tool and necessity for a runtime one.
- Matching a build tool is not matching an application shape. SvelteKit, Nuxt,
  Astro and SolidStart all build with Vite and none produces a directory a web
  server can hand out, so the Vite template excludes them explicitly.

Generated Dockerfiles must produce a container that needs **no writable root
filesystem** — the runtime sets `ReadonlyRootfs`. Writing an nginx config at
startup through the image's `envsubst` entrypoint made every static template
undeployable; they write it at build time now.

**Template renders are not cached.** The library is compiled into the binary, so
upgrading is how a broken template gets fixed, and a cache entry would keep
serving the broken one for its TTL. LLM answers are cached, in
`ai_dockerfile_cache`, keyed on a repo signature.

## Runtime

`internal/runtime`. `RUNNER_BACKEND` selects a backend and `docker` is the only
one — anything else is fatal at startup. Containers get a read-only root, tmpfs
for `/tmp`, `/run` and `/var/cache/nginx`, all capabilities dropped except
`NET_BIND_SERVICE`, `CHOWN`, `SETUID`, `SETGID`, `no-new-privileges`, a PID
limit, and `CONTAINER_MEMORY_MB` / `CONTAINER_CPU_LIMIT`.

**The liveness probe decides what "running" means.** Before reporting success
the runtime dials the container on the port from `EXPOSE`; on failure it tears
the container down, marks the deploy failed with a reason naming `PORT`, and
answers `422`, which the saga treats as terminal. It dials the address on the
**shared** network — a deploy container also joins its own network with
inter-container communication disabled, and that one is unreachable from here by
design.

A watchdog sweeps expired deploys: TTL expiry of un-aliased deploys, deploys
whose alias went idle past `ALIAS_IDLE_GC_DAYS`, and deploys beyond
`PROJECT_DEPLOY_RETENTION` per project. A deploy an alias points at is never
reclaimed.

## Projects, deploys and domains

A **project** is the permanent publish target. A **deploy** is one immutable
build at its own subdomain. A **custom domain** is an alias pointing at exactly
one deploy, so publishing and rolling back are the same operation — moving a
pointer — and neither rebuilds anything.

Attaching a domain returns a TXT challenge at `_snaphost-verify.<domain>`; an
in-process verifier flips `pending` → `verified` and re-checks periodically, so
a domain that stops pointing here loses verification. **Only a `verified` domain
routes.** Attach is refused while both `DOMAIN_CNAME_TARGET` and
`DOMAIN_A_RECORD_TARGET` are empty: there is no address to point DNS at.
`GET /internal/tls/authorize` answers whether a host is verified, but it is
currently inside the `WEBHOOK_SECRET`-protected internal group. Standard Caddy
`ask` cannot add that header, so this handler is not yet a working edge
integration by itself.

## Infra

- `infra/docker-compose.yml` — local development: the app, BuildKit, Traefik.
- `infra/docker-compose.prod.yml` — production: GHCR images pinned to a
  40-character Git SHA, a migrate service, BuildKit. No `build:`, no Traefik, no
  registry.
- [infra/deploy.sh](infra/deploy.sh) — `preflight` / `deploy <sha>` / `rollback`.
  Dumps the database with `sqlite3 .dump` before migrations, verifies the dump
  ends in `COMMIT;`, and refuses to roll back across a migration without
  `MIGRATIONS_BACKWARD_COMPATIBLE=true`. Never restores.
- [infra/backup.sh](infra/backup.sh) — scheduled dump on a systemd timer, shares
  `deploy.sh`'s lock, verifies the archive, encrypts with `age`, copies to S3,
  prunes on a retention policy that never touches a dump the deployment state
  references.

Changing any of those means running `infra/tests/deploy_test.sh` and
`infra/tests/backup_test.sh` (39 tests each). They need GNU coreutils and
`flock`, so on Windows run them in a Linux container. Their fakes are part of the
test: the `docker` fake refuses `up` for a service the manifest does not define,
because a `rollback_to` naming seven deleted services once passed the suite.

## Known gaps

Written down rather than fixed, so nobody rediscovers them:

- **Built images are never reclaimed.** See above.
- **Traefik, not Caddy.** Caddy is the recorded direction, but dynamic routing
  from a verified alias to a Docker container has not been implemented.
- **No end-to-end custom-domain TLS.** The verification handler exists, but its
  current webhook authentication is incompatible with standard Caddy `ask`;
  nothing in this tree safely bridges that boundary yet.
- **Trivy runs on every build.** The recorded decision is to put it behind a
  flag, since it defends against untrusted code and here the code is the
  operator's own.
- **Not installable.** [Task 7](docs/tasks/planned/0007-install-and-upgrade.md)
  is the install and upgrade story, and it is not started.

## Documents

- [docs/tasks/](docs/tasks/) — what is being done and why. Trust it over
  anything else when they disagree.
- [docs/architecture/](docs/architecture/) — overview, packages, deploy
  lifecycle, deployment model, security.
- [docs/decisions/](docs/decisions/) — current ADR status, including decisions
  inherited from upstream and later superseded here.
- [docs/operations/](docs/operations/) — backups, monitoring, rollback,
  local development, troubleshooting.
