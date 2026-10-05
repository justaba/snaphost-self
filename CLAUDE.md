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

`internal/` holds the application components:

| Package | Responsibility |
| --- | --- |
| `httpapi` | Middleware chain, RBAC, WebSocket log endpoint. No business logic. |
| `control` | Sessions, API keys, deploys, projects, custom domains, the saga, the operator audit log. Owns the schema. |
| `builder` | Clone or unpack, detect, generate and validate a Dockerfile, build. |
| `runtime` | Starts and stops user containers; the TTL watchdog. |
| `ai` | Dockerfile templates first, an LLM when none match. |
| `panel` | The operator UI, embedded with `go:embed`. |
| `shared` | Session constants and Dockerfile validation. |
| `wiring` | Narrow adapters between domain interfaces and concrete repositories or services. |
| `logbus`, `buildevents` | In-process fan-out that replaced Redis pub/sub and streams. |
| `uploads`, `gitcreds` | File-backed upload staging; in-memory credentials with a TTL. |
| `memlimit` | Reads the cgroup limit and derives `GOMEMLIMIT` from it. |

`wiring` keeps package dependencies narrow: saga uses `BuildScheduler` and
`Runtime`, while the container runtime uses `DeploymentStore`. All are direct
in-process calls. Keep new cross-package calls behind small domain interfaces.

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

LLM Dockerfile generation is an explicit optional fallback. `LLM_ENABLED`
defaults to `false`, including when a legacy `OPENROUTER_API_KEY` remains in
the env file. Only `LLM_ENABLED=true` requires a provider key. With AI disabled,
project Dockerfiles and built-in templates work; an unsupported project fails
permanently with a hint to add a Dockerfile, without a provider request.

**The Compose project is named in the compose file** (`name: snaphost-self`), not
passed on the command line, so every invocation agrees on it. If you run
`docker compose` by hand from `infra/`, pass `-p snaphost-self`: the original
`snaphost` checkout sits next door with its compose in `infra/` too, and without
a project name both take the directory name and share volumes.

### Verification before a commit

```
cd snaphost-backend && go build ./... && go vet ./... && go test ./...
gofmt -l cmd internal          # must print nothing
cd web && pnpm install --frozen-lockfile && pnpm test && pnpm lint
pnpm format:check && pnpm build
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
WAL). Migrations live in
[internal/control/db/migrations/](snaphost-backend/internal/control/db/migrations/).

**A schema change is a new migration. Never edit an applied one.** This rule
replaced the opposite one, and the reversal is worth understanding rather than
just obeying. The old rule — "nobody has this installed, so a new column goes
into the baseline" — was true while every database was built from nothing. It
stopped being true the first time a database recorded version 1, which now
includes the development one.

`golang-migrate` applies only migrations *above* the stored version, so an
edited `0001` never runs again. The failure is not a startup error: the process
comes up cleanly and the first request touching the changed table answers
`no such column: image_deleted_at`. Nothing in a from-scratch test can see it,
because there an edited baseline and a real migration are indistinguishable —
which is why
[upgrade_test.go](snaphost-backend/internal/control/db/upgrade_test.go) migrates
to a specific version first and then upgrades. **A schema change needs a test
in that file**, or it is only tested on installs that do not exist yet.

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
`custom_domains`, `api_keys`, `ai_dockerfile_cache`, `ai_usage_log`,
`admin_audit_log`.

## Request lifecycle — the order is load-bearing

[cmd/snaphost/main.go](snaphost-backend/cmd/snaphost/main.go) registers handlers
in a precise order:

1. **Before any middleware:** `GET /health`, `GET /metrics`, and
   `GET /ws/logs/:id`. Health must answer when authentication is broken, and a
   browser cannot put a header on a WebSocket handshake, so that endpoint
   authenticates itself.
2. **Middleware chain:** `Recovery` → `CORS` → `RequestID` → `Logger` →
   **`panel`** → `Auth` → `Casbin` → `Enrich` → upload body limit.
3. **Routes:** `/api/v1/*` registered directly — no proxy, no internal service API.

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
  in `internal/shared` because auth writes it and HTTP middleware reads it.
- **API keys:** `sk_` bearer tokens, a separate path for non-browser clients.
  `middleware.Auth` accepts exactly two credentials and a malformed
  `Authorization` header does **not** fall through to the cookie.
- **Login rate limit:** ten failures per address per five minutes, in memory.
  Only failures count; a success clears the counter.

`PublicRoutes` in [middleware/auth.go](snaphost-backend/internal/httpapi/middleware/auth.go)
is `POST /api/v1/auth/login`, `/health`, `/metrics` — that is all. Both `Auth`
and `Casbin` consult it.

## RBAC — Casbin with `keyMatch2`

[rbac_model.conf](snaphost-backend/internal/httpapi/rbac_model.conf) +
[rbac_policy.csv](snaphost-backend/internal/httpapi/rbac_policy.csv). Two roles:
`user` and `admin`, with `g, admin, user`. The role comes from the `users.role`
column, not a token claim.

Enforcement is against `c.Request.URL.Path` — the real URL with IDs substituted
— not Gin's `FullPath`; the policy file uses `:id` placeholders that `keyMatch2`
resolves. **Adding a route means adding a policy line**, or it is refused for
every role.

## Projects, and why there is no admin console

**There is no `/api/v1/admin` surface and no Администрирование section.** Both
existed because the platform this forked from had many accounts and one
administrator over them; here those are the same person, so the console was a
second, role-gated copy of the dashboard's own screens. `internal/control/admin`
is deleted and every `admin` policy line went with it — a request to one of
those paths is a Casbin `403`. The one thing that survived the role gate is
`GET /api/v1/audit`, because reading a record of destructive actions is not the
same as performing them.

Project management lives on the ordinary dashboard, in
`internal/control/project`: `GET /api/v1/projects` for the list with its
counters, `DELETE /api/v1/projects/:id` for the one destructive action. That
deletion is the shape every future one follows:

1. **Host state goes before the rows that name it.** `PrepareProjectDeletion`
   loads a plan, the handler stops containers and removes images, and only then
   does the transaction run. After the commit there is nothing left to find a
   stranded container with, so the order cannot be the other way round.
2. **`StopStrict` and `RemoveImage` are both called, never one or the other.**
   Stopping does not release the image at all now — a stopped deploy has to
   stay startable. And `RunnerClient.Stop` reports success for a deploy the
   runtime refuses to recognise, which is the swallow that makes saga
   compensation safe to repeat and is exactly wrong here, so this caller uses
   `StopStrict`. `DockerBackend.Stop` propagates a failed `ContainerRemove` for
   the same reason: it used to log and return nil, which put two layers of "it
   probably worked" in front of an irreversible delete.
3. **The commit re-check is a whole-project assertion, not a status filter.**
   `Delete` takes the deploy set the plan enumerated and refuses if the project
   has gained anything outside it, or if any deploy is still `running`. A
   status filter missed the build that *completed* during the cleanup window —
   deploy and saga both end at `running`, which is terminal, so nothing matched
   it and its rows were deleted while its container ran. That window is up to
   five minutes of stopping containers and removing images.

   **The empty plan is a separate branch and has to be.** `id NOT IN (NULL)` is
   NULL in SQLite, not TRUE, so the clause silently matches nothing — a project
   with no deploys at plan time would have accepted any latecomer. The clause
   is dropped entirely when the plan is empty.
4. **`Stop` reports the network too, and reaches it even when the container is
   already gone.** The network name derives from the deploy id, so once the
   rows are gone nothing can reconstruct it and no sweep can find it. An
   absent container used to return success immediately — which is exactly the
   state a retry after a partial cleanup finds, so the attempt that *could*
   have removed the network was the one that skipped it. `DestroyNetwork`
   treats a missing network as success, so compensation still re-runs safely.

   The teardown path sits behind `teardownClient` in
   [teardown.go](snaphost-backend/internal/runtime/backend/docker/teardown.go)
   so it can be tested. It has produced three leaks — a swallowed container
   removal, a swallowed network removal, and a network never attempted — and
   none were visible until the Docker client had a seam.
5. **The audit row is written by the same transaction as the deletion**, into
   `admin_audit_log`, with the counts and slug the deleted rows would otherwise
   take with them. There is no window where the rows are gone and the record of
   who removed them is not.
6. **The cleanup context is `context.Background()` with a timeout**, not the
   request's. A browser disconnect must not abandon a half-finished teardown.

The screen it lives on is **Проекты** — the dashboard's own, and the only place
projects are managed. The list carries `running_count`, because that number is
the difference between deletion reclaiming disk and deletion taking a live site
down, and the confirmation dialog says so.

The collection and the item need separate policy lines — `keyMatch2` does not
let `/api/v1/projects` borrow the `DELETE` on `/api/v1/projects/:id`, which is
the behaviour the RBAC test pins. There is also a test asserting the absence of
every `/api/v1/admin` path: a policy line outliving its handler is exactly how
the panel ended up with a Транзакции tab pointing at a deleted page.

## The deploy saga

Orchestrated in `internal/control/saga`, in-process, over a buffered channel.
`deploy_sagas.current_step` is the durable state: `pending` → `building` →
`built` → `provisioning` → `running`, with `compensating` / `compensated` /
`failed` on the way out.

1. `POST /api/v1/deploys` resolves or creates the project, writes the deploy and
   saga rows, enqueues, returns `202`.
2. The worker enqueues a build. The builder clones (or unpacks an upload),
   detects the project, gets a Dockerfile — template first, LLM only if none
   matches — builds with BuildKit and loads the image into Docker.
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

No vulnerability scanner is bundled into the service or run in the build
pipeline. This fork assumes the self-hosting operator trusts the submitted
source and base images; operators with a different trust boundary must scan in
their own CI or image-admission flow.

The watchdog reclaims local images after deploy deletion or expiry. With no
registry, the disk it protects is the one the platform runs on.

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

`internal/runtime` owns the Docker container lifecycle. Containers get a read-only root, tmpfs
for `/tmp`, `/run` and `/var/cache/nginx`, all capabilities dropped except
`NET_BIND_SERVICE`, `CHOWN`, `SETUID`, `SETGID`, `no-new-privileges`, a PID
limit, and `CONTAINER_MEMORY_MB` / `CONTAINER_CPU_LIMIT`.

**The liveness probe decides what "running" means.** Before reporting success
the runtime dials the container on the port from `EXPOSE`; on failure it tears
the container down, marks the deploy failed with a reason naming `PORT`, and
returns a permanent operation error, which the saga compensates. It dials the address on the
**shared** network — a deploy container also joins its own network with
inter-container communication disabled, and that one is unreachable from here by
design.

A watchdog sweeps expired deploys: TTL expiry of un-aliased deploys and deploys
beyond `PROJECT_DEPLOY_RETENTION` per project. A deploy an alias points at is
never reclaimed.

**The same watchdog reclaims images, and the two sweeps are deliberately
independent.** With no registry the disk that fills is the one the platform
runs on. The second sweep asks for deploys at `failed` or `deleted` that still
hold an `image_ref` with no `image_deleted_at`, and removes each from the
daemon.

**`stopped` is not in that list, and that omission is a feature with a
deadline.** A stopped deploy is one somebody paused or whose TTL ran out, and
its image is what makes `POST /api/v1/deploys/:id/start` a container run rather
than a rebuild. Reclaiming it would make every stop irreversible.

The deadline is `sweepStopped`, the third sweep, and it exists because without
it the favour never expires. `MarkDeleted` has exactly one caller — the delete
button — so nothing automatic moved a deploy out of `stopped`, and the TTL and
retention sweeps both *end* there. Sparing `stopped` and having no way out of
it is a disk leak with no ceiling: every preview that ever expired keeps a
container image forever, which is the problem image GC was added to solve.
`STOPPED_IMAGE_GRACE_HOURS` (default 168) is how long the image is kept; an
alias-published deploy is never touched.

The grace is measured from `stopped_at`, and two things about that column are
load-bearing:

- **not `COALESCE(stopped_at, updated_at)`** — a trigger rewrites `updated_at`
  on every write, so a row touched for any reason would have its clock reset
  and might never age out;
- **`SetRunning` clears it.** `stopped_at` is written with its own `COALESCE`,
  so it records the *first* stop and never moves — correct when nothing could
  restart a deploy, wrong the moment something can. A deploy stopped in
  January, restarted, and stopped again in June would be measured from January
  and lose its image on the next tick.

Three more things about it are load-bearing:

- **`deploys.image_deleted_at` is the marker, not a nulled `image_ref`.** The
  reference stays for diagnostics, so the column pair says both "what was
  built" and "is it still on disk". A `COALESCE` keeps the first timestamp, so
  a retry does not restamp the row.
- **It is written only after Docker confirms the image is gone.** A marker
  ahead of the removal is disk that is recorded as reclaimed and is not, and no
  sweep will ever look at that row again.
- **The moment an image exists on the host, the database says so.** The
  pipeline calls `ReportImageLoaded` immediately after the daemon load, before
  anything else can fail the deploy;
  `saga.MarkImageBuilt` then records it again on the success path. Both write
  `deploys.image_ref`, which is the only column the sweep reads. Writing it
  only on the success path would leave any artifact followed by a later error
  unnamed and permanently invisible to cleanup.
- **The invariant is: either the database names the image, or the image is not
  on the host.** `recordLoadedImage` retries on a fresh bounded context — the
  likely reason the first attempt fails is a cancelled build context, and
  retrying on that same context cannot help — and if that also fails it
  *removes the image*, on a third bounded context, before failing the build
  Permanently.

  Two earlier answers were wrong and are worth not repeating. Logging and
  continuing allows a later pipeline error to orphan the artifact. Returning
  `Transient` is no better, because it reasons from machinery that does not
  exist — **nothing retries a build.** `runBuildWorker` logs `IsTransient` and
  calls `FinalizeAsFailed` either way; a retry budget is still unimplemented.
  Any code here that assumes a retry will happen is wrong.

  The residual: if the database and the Docker daemon are both unreachable,
  nothing durable can be written anywhere — a deferred-cleanup row would need
  the database that just refused — so the image is orphaned and an ERROR line
  names it with the exact `docker image rm` to run.
- **The three sweeps do not share a failure path.** A broken expiry query
  would otherwise silently take image reclamation with it, and this repository
  has already shipped a reclaim statement that never once executed.

**Stopping and deleting are different endpoints, and conflating them is the
bug this design keeps inviting.** `POST /deploys/:id/stop` tears the container
down and records `stopped`, keeping the image. `DELETE /deploys/:id` marks the
row `deleted` and releases it. The panel had one button wired to `DELETE` and
labelled «Остановить», so pausing a site destroyed it — and once image GC
landed, destroyed the artifact `start` needs. If a caller ever routes a stop
through `DELETE` again, `start` begins answering `image_reclaimed` for
everything and the button silently becomes decoration.

`start` claims the row by moving it `stopped` → `provisioning` with a guarded
`UPDATE` before it calls the runtime. That claim is what stops two clicks
starting two containers for a row that records one, and it is why
`provisioning` is in the runtime's `deployableStatuses`. A refusal after the
claim must call `AbandonRestart`: nothing sweeps `provisioning`, so a leaked
claim leaves the deploy looking like it is starting forever.

BuildKit owns its cache separately from Docker images. Both daemon configs
enable automatic OCI-worker GC with `reservedSpace = "512MB"`,
`maxUsedSpace = "4GB"` and `minFreeSpace = "5GB"`. These are periodic targets,
not a hard quota during an active build. Inspect or emergency-prune this daemon
with `buildctl` inside the `buildkitd` service; `docker builder prune` targets
Docker's builder and is the wrong ownership boundary here.

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
The application intentionally exposes no generic `/internal` HTTP surface.
Production edge traffic uses separate listeners instead: `/tls/ask` on 8082
and a Host-resolving proxy on 8081. Both remain internal on the Compose control
network; only Caddy publishes 80/443. Do not move either into the former service
webhook API or give Caddy the Docker socket.

## Infra

- `infra/docker-compose.yml` — local development: the app, BuildKit, Traefik.
- `infra/docker-compose.prod.yml` — production: a GHCR image pinned to a
  `vMAJOR.MINOR.PATCH` release, a migrate service, BuildKit and digest-pinned
  stock Caddy as the sole owner of 80/443. No Compose `build:`, no Traefik,
  no site registry. The
  app healthcheck runs inside its runtime image.
- [infra/deploy.sh](infra/deploy.sh) — `preflight` / `deploy <version>` /
  `rollback`.
  Dumps the database with `sqlite3 .dump` before migrations, verifies the dump
  ends in `COMMIT;`, waits for Docker health rather than mere process state,
  persists the successful version in the env/state files, and refuses to roll
  back across a migration without `MIGRATIONS_BACKWARD_COMPATIBLE=true`. Never
  restores.
- [infra/backup.sh](infra/backup.sh) — scheduled dump on a systemd timer, shares
  `deploy.sh`'s lock, verifies the archive, optionally encrypts with `age` and
  uploads to S3,
  prunes on a retention policy that never touches a dump the deployment state
  references.

Changing any of those means running `infra/tests/deploy_test.sh` (62 tests),
`infra/tests/backup_test.sh` (41) and, for the host release contract,
`infra/tests/snaphostctl_test.sh` (31). They need GNU coreutils and
`flock`, so on Windows run them in a Linux container. Their fakes are part of the
test: the `docker` fake refuses `up` for a service the manifest does not define,
because a `rollback_to` naming seven deleted services once passed the suite.

## Known gaps

Written down rather than fixed, so nobody rediscovers them:

- **A failed deploy cannot be retried from the panel.** Its image was never
  usable and the sweep reclaims it, so the only way forward is deploying the
  project again. The button that used to say «Перезапустить» called an endpoint
  this backend has never had.
- **Traefik remains local-only.** Production Caddy routes verified project
  domains through the monolith without a Docker socket. Local development may
  enable generated Traefik hosts with `DEV_DOMAIN_SUFFIX`.
- **The edge is accepted.** Compose Caddy owns 80/443 and serves verified
  project domains with fail-closed on-demand TLS. Public staging and production
  DNS/ACME checks and encrypted local TLS-state recovery are recorded in
  [Task 4](docs/tasks/completed/0004-production-edge.md).
- **Install code exists; production proof is partial.**
  [infra/snaphostctl](infra/snaphostctl) implements first install,
  checkout-aware upgrade and coordinated rollback. Its fake-command suite is
  [infra/tests/snaphostctl_test.sh](infra/tests/snaphostctl_test.sh), and the
  operator contract is [the runbook](docs/operations/install-and-upgrade.md).
  The [first VPS rehearsal](docs/operations/rehearsals/2026-09-04-vps.md)
  covers real install/upgrade/rollback, local restore and a 4 GB cold build.
  The [1.9 GiB VDS Node rehearsal](docs/operations/rehearsals/2026-10-05-task7-vds-node.md)
  passed Vite and pinned React/Vite 8 builds; the older Vite 6 workload exposed
  OOM at 768 MiB and 1 GiB BuildKit limits. The
  [isolated 1 GiB arm64 drill](docs/operations/rehearsals/2026-10-05-task7-1g.md)
  passed a fresh supported-layout install, the React build with a 448 MiB
  BuildKit limit and encrypted scheduled SQLite restore with login/password
  rotation. The installer now sizes BuildKit's initial memory limit from RAM.
  [Task 7](docs/tasks/planned/0007-install-and-upgrade.md) stays in progress
  until install, upgrade and rollback against public GitHub/GHCR artifacts are
  recorded. The 1 GiB VM used a local registry and arm64; CI images target amd64.

## Documents

- [docs/tasks/](docs/tasks/) — what is being done and why. Trust it over
  anything else when they disagree.
- [docs/architecture/](docs/architecture/) — overview, packages, deploy
  lifecycle, deployment model, security.
- [docs/decisions/](docs/decisions/) — current ADR status, including decisions
  inherited from upstream and later superseded here.
- [docs/operations/](docs/operations/) — backups, monitoring, rollback,
  local development, troubleshooting.
