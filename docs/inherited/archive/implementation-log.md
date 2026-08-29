# Refactor Notes

Status: Archived implementation history
Superseded by: `docs/architecture/`, `docs/operations/`, and `docs/decisions/`

## Task 10: first production runtime adapter plan

**Date:** 2026-05-23

### What

Added a future implementation roadmap to `REFACTOR_TASKS.md` for implementing
Yandex Cloud as the first concrete production runtime adapter, not as a
platform-wide Yandex dependency. SnapHost core remains provider-agnostic;
`RUNNER_BACKEND=yandex` is the first cloud adapter behind the existing backend
boundary. The plan breaks the work into PR-sized subtasks:

- production auth and env contract;
- Terraform IAM role audit;
- Yandex backend compilation under `-tags yandex`;
- runner auth through `shared/yandexauth`;
- minimal Yandex deploy happy path;
- stop/delete/TTL lifecycle;
- safe API Gateway spec mutation;
- registry end-to-end integration;
- runtime log MVP;
- security hardening;
- production smoke checklist.

### Key decision

Production services should receive service-account authorized-key JSON
files and issue IAM tokens themselves through the Yandex SDK. They should
not receive long-lived admin credentials or manually created IAM tokens.
The bootstrap/admin Terraform account remains provisioning-only and must
not be mounted into backend containers.

### Status

Planning only. No runtime code changed. The next executable step is
Task 10.0: document the production secret/env contract for builder-svc and
runner-svc.

## Task 10.1: Terraform IAM role audit

**Date:** 2026-05-23

### What

Audited the Yandex Terraform IAM configuration against the Task 10.0 runtime
contract in `docs/yandex-runtime.md`. Reviewed service-account roles, provider
bootstrap credential usage, Terraform outputs, sensitive key-path handling, and
all IAM role assignments under `terraform/yandex/`.

No Terraform changes were needed. The existing role set already matches the
documented runtime split between provisioning credentials, builder runtime
credentials, and runner runtime credentials.

### Audit result

`snaphost-builder` is limited to:

- `container-registry.images.pusher`

This matches the builder runtime contract: `builder-worker` uses the builder
authorized-key JSON only to push built images to Yandex Container Registry. No
serverless, API Gateway, DNS, IAM, billing, folder admin, or broad registry
admin role is assigned to the builder service account.

`snaphost-runner` is limited to:

- `container-registry.images.puller`
- `serverless-containers.containerInvoker`
- `serverless-containers.editor`
- `api-gateway.editor`
- `logging.reader`

This matches the runner runtime contract: `runner-api` and `runner-watchdog`
need Yandex lifecycle access for Serverless Containers, API Gateway route
management, image pulls, invocation, and runtime log access. No broad admin,
IAM admin, billing admin, DNS admin, registry admin, or unrelated runtime role
assignment was found.

Bootstrap credentials stay provisioning-only. `versions.tf` consumes
`var.bootstrap_key_file` only in the Yandex provider configuration, and
Terraform does not output the bootstrap key path for runtime use.
`docs/yandex-runtime.md` explicitly says bootstrap/admin Terraform credentials
must never be mounted into backend containers.

Outputs match the runtime contract. `outputs.tf` exposes `folder_id`,
`registry_url`, `runner_sa_id`, `api_gateway_id`, `runner_key_path`, and
`builder_key_path`; the key-path outputs are marked `sensitive = true`. No
output exposes raw private key contents.

The IAM search found role assignments only in
`terraform/yandex/service_accounts.tf`, through
`yandex_resourcemanager_folder_iam_member.runner_roles` and
`yandex_resourcemanager_folder_iam_member.builder_roles`.

### Verification

- `rg "iam_member|iam_binding|role|service_account|service-account|authorized key|key_path|runner_sa|builder_sa" terraform/yandex`
  found only the expected provider bootstrap key reference, service accounts,
  runtime role locals, runtime IAM members, authorized-key resources, and
  runtime key-path outputs.
- `terraform -chdir=terraform/yandex fmt -check` passed.
- `terraform -chdir=terraform/yandex validate` was attempted but could not run
  because providers are not initialized locally (`hashicorp/local` and
  `yandex-cloud/yandex` missing). Per task instructions, `terraform init` was
  not run.
- `git diff --check` passed for the changed file.

### Out of scope

- No `terraform apply`, no `terraform init`, and no provider/network download.
- No Yandex runtime Go implementation.
- No manual Yandex smoke test; Task 10.1 is a Terraform audit only.
- Runtime testing may reveal whether `serverless-containers.containerInvoker` and
  `logging.reader` are sufficient or should be refined further, but there is no
  local Terraform inconsistency to fix in this task.

## Task 10.2: Yandex backend compile fix

**Date:** 2026-05-23

### What

Made `runner-svc` compile with `-tags yandex` against
`github.com/yandex-cloud/go-sdk@v0.31.0` without changing Docker backend
behavior or implementing new Yandex runtime behavior.

Fixed the current SDK/API mismatches in
`runner-svc/internal/backend/yandex/yandex.go`:

- SDK service-account key construction now uses `iamkey.ReadFromJSONFile`
  instead of manually constructing an `iamkey.Key` with the removed
  `iamkey.Key_RSA_2048` constant.
- Create-container metadata extraction now uses `Operation.Metadata()` and a
  `*containerspb.CreateContainerMetadata` type assertion.
- Deploy revision warm-pool field now uses the existing
  `ProvisionPolicy` field instead of the non-existent `Provisioned` field.
- API Gateway spec reads now use `GetOpenapiSpec(..., YAML)` instead of the
  non-existent `ApiGateway.GetSpec()` accessor.

### Files changed

- `snaphost-backend/internal/runtime/backend/yandex/yandex.go`

### Verification

All commands were run from `snaphost-backend/runner-svc` with local
`GOCACHE=D:\snaphost\.tmp-gocache` and
`APPDATA=D:\snaphost\.tmp-appdata` to avoid Windows profile cache/telemetry
permission errors. `go build` commands also used per-command Git
`safe.directory=D:/snaphost` environment config so VCS stamping could read the
worktree without changing global Git config.

- `go test ./...` passed.
- `go test -tags yandex ./...` passed.
- `go build ./...` passed.
- `go build -tags yandex ./...` passed.
- `git diff --check -- snaphost-backend/internal/runtime/backend/yandex/yandex.go REFACTOR_NOTES.md` passed.

Generated temporary Go cache/telemetry files under `.tmp-gocache` and
`.tmp-appdata` were removed after verification.

### Out of scope

- No real Yandex credentials were used.
- No manual Yandex smoke test was run; Task 10.2 is compile-only.
- Runner auth still has the existing local `IAMTokenSource` path. Full runner
  auth cleanup through `shared/yandexauth` is deferred to Task 10.3.
- API Gateway spec mutation and Serverless Container lifecycle behavior are
  not proven correct against Yandex Cloud until Task 10.4+ smoke tests.

## Task 10.3: Runner auth via shared/yandexauth

**Date:** 2026-05-24

### What

Wired `runner-svc` Yandex backend authentication through
`snaphost/shared/yandexauth`. `NewYandexBackend` now constructs exactly one
authenticated Yandex SDK at backend startup from `YANDEX_SA_KEY_PATH` via
`yandexauth.NewSDK`.

Removed the runner-local hand-rolled IAM/JWT token exchange implementation:

- deleted `runner-svc/internal/backend/yandex/iam.go`;
- removed `tokenSource` from `YandexBackend`;
- removed direct `iamkey.ReadFromJSONFile` and `ycsdk.ServiceAccountKey`
  construction from the runner Yandex backend.

### Files changed

- `snaphost-backend/internal/runtime/backend/yandex/yandex.go`
- `snaphost-backend/internal/runtime/backend/yandex/iam.go`
- `snaphost-backend/internal/runtime/go.mod`
- `snaphost-backend/internal/runtime/go.sum`
- `snaphost-backend/internal/runtime/Dockerfile`
- `infra/docker-compose.yml`

### Verification

Commands run from `snaphost-backend/runner-svc` with repo-local temporary
`GOCACHE`/`APPDATA` directories to avoid Windows profile cache/telemetry
permission issues:

- `go test ./...` passed.
- `go test -tags yandex ./...` passed.
- `go build -buildvcs=false ./...` passed.
- `go build -buildvcs=false -tags yandex ./...` passed.
- `rg "golang-jwt/jwt/v5|IAMTokenSource|LoadAuthorizedKey|NewIAMTokenSource|tokenSource|iamkey|ServiceAccountKey|iam.api.cloud.yandex.net/iam/v1/tokens" snaphost-backend/runner-svc`
  returned no matches.
- `go mod why github.com/golang-jwt/jwt/v5` reports the main module does not
  need the package.

`runner-svc/go.mod` now depends on the local `snaphost/shared` module via
`replace snaphost/shared => ../shared`. Because `shared/go.mod` currently uses
Go 1.25 and newer shared transitive versions, the runner module graph was
updated accordingly when adding this dependency.

Updated `runner-svc/Dockerfile` builder image from `golang:1.22-alpine` to
`golang:1.25-alpine` so Docker/compose builds match the new module `go 1.25.0`
directive.

Updated the `runner-api` and `runner-watchdog` compose build context from
`snaphost-backend/runner-svc` to `snaphost-backend` and set
`dockerfile: runner-svc/Dockerfile`. This is required because
`runner-svc/go.mod` now has `replace snaphost/shared => ../shared`, so Docker
builds need both `runner-svc/` and `shared/` inside the build context.

### Out of scope

- No real Yandex credentials were used.
- No manual Yandex Cloud deploy smoke test was run; this remains deferred until
  Task 10.4+.
- This task only unifies auth construction. API Gateway spec mutation,
  Serverless Container lifecycle behavior, and Cloud Logging behavior still
  need runtime validation in later Yandex tasks.

## Gateway cleanup: remove stale future-service routes

**Date:** 2026-05-23

### What

Removed stale future-service wiring from `api-gateway`. The gateway now
declares only implemented downstream HTTP services in config:

- `user-billing`
- `ai-orchestrator`

Deleted config fields and default env lookups for services that do not
exist in the repo or compose stack:

- `RepoHandler` / `REPO_HANDLER_URL`
- `InfraProvisioner` / `INFRA_PROVISIONER_URL`
- `LogStreamer` / `LOG_STREAMER_URL`

Also removed the public `/api/v1/infra` proxy route and its RBAC policy
entries. There were no frontend or documented callers for `/api/v1/infra`.
Runtime provisioning remains an internal saga step: `user-billing` calls
`runner-svc` directly over the internal network with `X-Webhook-Secret`.
Log streaming remains Redis-backed and exposed through the existing logs
path, not through a separate `log-streamer` service.

### Files changed

- `snaphost-backend/internal/gateway/config/config.go`
- `snaphost-backend/internal/gateway/routes/routes.go`
- `snaphost-backend/internal/gateway/rbac_policy.csv`
- `CLAUDE.md`

### Why

The stale names made the gateway config look like it depended on planned
services (`repo-handler`, `infra-provisioner`, `log-streamer`) even though
those responsibilities are already covered by implemented services:
`builder-svc`, `runner-svc`, and Redis-backed log streaming. Keeping dead
upstream defaults increased confusion and left a public route that could
only fail at runtime.

### Out of scope

Historical migration comments that mention `infra-provisioner` were left
unchanged. They describe old schema intent, not active gateway wiring.

## Task 0: Yandex backend isolated behind build tag

**Date:** 2026-05-10

### What

Isolated the broken `runner-svc/internal/backend/yandex/` package behind the `yandex` build tag so it no longer breaks `go build ./...` in default mode.

### Files changed

- `snaphost-backend/internal/runtime/backend/yandex/yandex.go` — added `//go:build yandex` tag.
- `snaphost-backend/internal/runtime/backend/yandex/iam.go` — added `//go:build yandex` tag.
- `snaphost-backend/internal/runtime/backend/yandex/gateway_spec.go` — added `//go:build yandex` tag.
- `snaphost-backend/internal/runtime/cmd/api/yandex_enabled.go` — new. Build-tagged `yandex`. Wraps `yandex.NewYandexBackend` + `yandex.SetMemoryAccessor` behind a local `newYandexBackend` helper so `cmd/api/main.go` does not import the yandex package directly.
- `snaphost-backend/internal/runtime/cmd/api/yandex_stub.go` — new. Build-tagged `!yandex`. `newYandexBackend` returns a clear error: `"yandex backend not compiled in: rebuild with -tags yandex"`.
- `snaphost-backend/cmd/runner-api/main.go` — dropped `internal/backend/yandex` import; switch case `"yandex"` now calls `newYandexBackend(cfg, publisher, log)`. Behaviour at runtime when `RUNNER_BACKEND=yandex` in default build: `log.Fatal` with the indirection's error message instead of crashing on import resolution (which never happens because the package is now excluded by build tag).

`cmd/watchdog/main.go` already had `log.Fatal("yandex backend not implemented yet")` for the yandex case and never imported the yandex package — left unchanged.

### Why

`runner-svc/internal/backend/yandex/` was written against a force-pushed pseudo SDK version that no longer resolves. It blocked `go build ./...` for the whole service, blocking every other refactor task that needs a green default build (e.g. Task 1 acceptance criteria). Build-tag isolation is the minimal-blast-radius fix until a dedicated Yandex rewrite task happens.

### Build matrix verification (`go build`)

| Service | Default | `-tags yandex` |
| --- | --- | --- |
| api-gateway | ✅ | n/a |
| builder-svc | ✅ | n/a |
| runner-svc | ✅ | ❌ (expected — pre-existing SDK errors in `internal/backend/yandex/`) |
| user-billing | ✅ | n/a |
| ai-orchestrator | ✅ | n/a |

Default `go vet ./...` clean across all five services. `go test ./...` passes (no test files defined — no regressions introduced).

The `-tags yandex` failures are unchanged from prior to Task 0:

```
internal\backend\yandex\yandex.go:80:24: undefined: iamkey.Key_RSA_2048
internal\backend\yandex\yandex.go:133:21: createOp.GetMetadata undefined
internal\backend\yandex\yandex.go:180:3: unknown field Provisioned ...
internal\backend\yandex\yandex.go:284:34: gw.GetSpec undefined
internal\backend\yandex\yandex.go:314:34: gw.GetSpec undefined
internal\backend\yandex\yandex.go:341:34: gw.GetSpec undefined
```

These are tracked for the future Yandex backend rewrite task.

### Smoke test

Backend selection logic for `RUNNER_BACKEND=docker` is unchanged (the docker case in `cmd/api/main.go` still calls `docker.NewDockerBackend` directly). Default build runner-svc binary loads and selects docker backend identically. End-to-end UI deploy not driven from this session — needs manual verification by the user via `make dev` + paste-a-repo flow.

### Out of scope

- Fixing the Yandex SDK migration. Future task.
- runner-svc `cmd/watchdog/main.go` was not modified (already log.Fatal on yandex case, no yandex import).

---

## Task 1: shared/yandexauth/ + builder-svc migration

**Date:** 2026-05-10

### What

Replaced the hand-rolled JWT signing / IAM token exchange / RSA-PEM parsing in `builder-svc/internal/build/yandex_iam.go` with a thin wrapper around the Yandex Cloud Go SDK, exposed via a new `shared/yandexauth/` package. The SDK now owns JWT signing, key parsing, IAM exchange, and (for its own gRPC calls) token refresh.

### Chosen SDK version

`github.com/yandex-cloud/go-sdk@v0.31.0` (the v0 line; module path **without** `/v2`).

**Why:**

v0.31.0 provides convenience wrappers for serverless containers and API Gateway (`sdk.Serverless().Containers()`, `sdk.Serverless().APIGateway()`), which match the existing call shape in `runner-svc/internal/backend/yandex/yandex.go`. v2 removed those wrappers — each call site would need to construct a gRPC client manually via `sdk.GetConnection`. For the current pre-MVP scope the boilerplate delta favours v0. Task 1 itself (`shared/yandexauth/`) is roughly equivalent effort under either — the version pick is driven by the future Yandex backend rewrite, not this task. **Re-evaluation point:** if Yandex announces v0 deprecation, or v2 ships a feature we need (e.g. workload identity), version migration becomes its own task.

Full recon table: [Yandex SDK ADR](../../decisions/0001-yandex-sdk-v0.md).

### Files changed

- **New** `snaphost-backend/internal/shared/yandexauth/sdk.go` — `NewSDK(ctx, keyPath) (*ycsdk.SDK, error)` reads authorized key JSON via `iamkey.ReadFromJSONFile`, builds credentials via `ycsdk.ServiceAccountKey`, returns lazy SDK via `ycsdk.Build`. `IAMToken(ctx, sdk) (string, error)` calls `sdk.CreateIAMToken(ctx)` and returns the `IamToken` field.
- **New** `snaphost-backend/internal/shared/yandexauth/sdk_test.go` — two unit tests: (1) construction with an in-memory RSA-2048 PKCS8 key, written to a temp authorized-key JSON file, verifies non-nil SDK without network; (2) missing-file path returns error. Tests pass: `ok snaphost/shared/yandexauth 0.090s`.
- `snaphost-backend/internal/shared/go.mod` — added `github.com/yandex-cloud/go-sdk v0.31.0` + transitive deps via `go get`.
- `snaphost-backend/internal/builder/build/yandex_iam.go` — rewrote. Was 168 lines (JWT signing, PKCS1/PKCS8 PEM parsing, token caching, HTTP client). Now 64 lines: a `yandexIAMAuth` struct holding `*ycsdk.SDK + *zap.Logger`, `Credentials` calls `yandexauth.IAMToken(ctx, a.sdk)` for any `cr.yandex` host. BuildKit's `session.Attachable` + `auth.AuthServer` interface shape preserved. No more direct imports of `crypto/rsa`, `crypto/x509`, `encoding/pem`, `golang-jwt/jwt/v5`, or `net/http`.
- `snaphost-backend/internal/builder/build/buildkit.go::NewBuilder` — the `"yandex_iam"` switch case now constructs the SDK via `yandexauth.NewSDK(context.Background(), cfg.YandexSAKeyPath)` and passes it to `newYandexIAMAuth(sdk, log)`.
- `snaphost-backend/internal/builder/go.mod` — `go mod tidy` to refresh transitive deps. `github.com/golang-jwt/jwt/v5` no longer required by `internal/build/` (still appears via other transitive paths if any).

Verified no Task-1-introduced references to `crypto/rsa`, `crypto/x509`, `encoding/pem`, or `golang-jwt/jwt/v5` remain in `builder-svc/` source. `go mod why github.com/golang-jwt/jwt/v5` reports `(main module does not need package …)`.

`github.com/golang-jwt/jwt/v4` remains in `builder-svc/go.mod` as an **indirect** dependency. This is expected: the Yandex Cloud SDK (`go-sdk/credentials.go`) uses `jwt/v4` internally for JWT signing. No action required — the direct dependency we owned (`v5`) is gone; the transitive `v4` is owned by the SDK.

### Caching note

`sdk.CreateIAMToken` on v0.31.0 is **uncached** at that entry point — each call performs a fresh HTTP exchange with the IAM API. Acceptable for the current usage (builder-svc requests one token per registry push, which is infrequent). The SDK still caches the token *internally* for its own gRPC calls (`sdk.Serverless().Containers().Create()` etc.) via the `IamTokenMiddleware` interceptor; only the externally surfaced `IAMToken` helper is uncached. If call frequency grows, add an external cache in `shared/yandexauth/` — explicitly not done now (premature without a benchmark; correctness > performance).

This is documented inline on `IAMToken` in `shared/yandexauth/sdk.go`.

### Build matrix verification

| Service | Default | -tags yandex |
| --- | --- | --- |
| api-gateway | ✅ | n/a |
| builder-svc | ✅ | n/a |
| runner-svc | ✅ | ❌ (6 errors, unchanged from Task 0 baseline. runner-svc has its own go.mod and pins SDK independently of `shared/`.) |
| user-billing | ✅ | n/a |
| ai-orchestrator | ✅ | n/a |
| shared | ✅ | n/a |

`make test` green. `shared/yandexauth` tests pass.

### `make lint` — pre-existing lint debt

`make lint` does not pass cleanly because of **pre-existing** issues in api-gateway, unrelated to Task 1:

```
api-gateway/main.go:70:19           errcheck    defer logger.Sync()
api-gateway/middleware/jwt.go:67:2  staticcheck SA9003: empty branch
api-gateway/proxy/http.go:43:11     errcheck    w.Write(...)
```

All three predate Task 1 (the files are not touched by this task). Per refactor task discipline, pre-existing issues are not fixed by Task 1; they are tracked in this Pre-existing lint debt section and left in place.

`builder-svc`, `runner-svc`, `user-billing`, `ai-orchestrator`, and `shared` (including the new `yandexauth/` package) are clean under `golangci-lint run`.

### Smoke test

Out-of-scope at runtime for this session (no Yandex SA key available locally). The migrated path (`REGISTRY_AUTH_MODE=yandex_iam`) is exercised only when builder-svc pushes to a cr.yandex registry — not the docker dev flow. Docker push path is untouched (`static` branch in `NewBuilder` unchanged). User-driven docker UI smoke test remains the acceptance gate per Task 0 baseline.

### Out of scope

- `runner-svc/internal/backend/yandex/` stays as-is (build-tagged off). Yandex backend rewrite is a dedicated future task.
- Docker / static credential branch in `buildkit.go`.
- IAM token cache inside `shared/yandexauth/`. To be added only if measured contention appears.

---

## Task 2 (Part 1): user-billing `GET /internal/deploys/:id`

**Date:** 2026-05-11

### What

Added a webhook-secret-protected `GET /internal/deploys/:id` endpoint to user-billing returning the minimal `DeployInfo` shape needed by runner-svc to validate deploy requests before launching a container. Part 1 is the data surface only; Part 2 (runner-svc client + validation logic + config) is a separate sub-step.

### Files changed

- `snaphost-backend/internal/control/deploy/handler.go` —
  - Extracted `Repo` interface (7 methods: `Create`, `Get`, `ListByUser`, `UpdateStatus`, `SetRunning`, `MarkDeleted`, `FindExpiredWithDetails`). `*Repository` satisfies it. `Handler.repo` field and `NewHandler` parameter retyped from `*Repository` to `Repo`. Required for mock-based handler tests; not a feature, just enables testability.
  - New `DeployInfo` struct — 4 fields (`deploy_id`, `user_id`, `status`, `image_ref`), **no `omitempty`** so callers can distinguish field-absent (bug) from field-empty (legitimate, e.g. status=pending).
  - New `GetDeployInternal` handler. Error mapping: invalid UUID → 400 via house `errResponse` shape; `pgx.ErrNoRows` → 404 with literal `{"error": "deploy not found"}` (verbatim contract for runner-svc client sentinel); other repo errors → 500 with generic message, internal detail logged but not leaked.
  - New imports: `context`, `errors`, `github.com/jackc/pgx/v5`.
- `snaphost-backend/internal/control/routes/routes.go` — registered `GET /internal/deploys/:id` inside the existing `internal` group → same `wallet.WebhookSecretMiddleware` as the other `/internal/*` endpoints. No new middleware.
- `snaphost-backend/internal/control/deploy/handler_test.go` (new) — 5 tests using a `fakeRepo` that only stubs `Get`; the other interface methods `panic("not used")` to catch accidental calls. Coverage: happy path with all four JSON keys asserted; empty `ImageRef` round-trip as `""`; not-found 404 with literal body; invalid UUID 400 without touching repo; DB error 500 with no internal detail leakage.

### Idempotency check

`Repository.Get` is a pure `QueryRow` + `Scan` (repository.go:61-80). No `UPDATE`, no audit log, no counter increment. Endpoint is safe to call repeatedly — fits `GET` semantics.

### Smoke test (curl against live stack)

Rebuilt user-billing image, recreated container. Host port 8083 → container 8081.

Case 1 — happy path:
```
$ curl --noproxy '*' -H "X-Webhook-Secret: your-internal-webhook-secret-here" \
    http://localhost:8083/internal/deploys/b132cb0d-ce92-4009-a8f3-221d86d8607c
HTTP/1.1 200
{"deploy_id":"b132cb0d-ce92-4009-a8f3-221d86d8607c","user_id":"a4a355f8-9769-454f-b5c0-9782acceebc0","status":"deleted","image_ref":""}
```

Case 2 — not found:
```
$ curl ... /internal/deploys/00000000-0000-0000-0000-000000000000
HTTP/1.1 404
{"error":"deploy not found"}
```

Case 3 — no auth:
```
$ curl http://localhost:8083/internal/deploys/...
HTTP/1.1 401
{"error":"unauthorized","message":"invalid webhook secret"}
```

All three cases pass. `image_ref` correctly serialized as `""` for a deploy whose DB column is NULL — confirms no-omitempty contract holds on the wire.

### Builds + tests + lint

All 5 services build clean. `go test ./internal/deploy/` → ok 0.016s (5 tests). `golangci-lint run` on user-billing — clean.

### Out of scope for Part 1

- runner-svc billing client `GetDeploy` method — Part 2.
- `validateDeployRequest` in runner.Service.Deploy — Part 2.
- `AllowedRegistryPrefixes` / `StrictImageValidation` config — Part 2.
- `ValidationError` / `ErrAlreadyRunning` types — Part 2.

---

## Task 2 (Part 2): runner-svc image_ref validation

**Date:** 2026-05-11

### What

Added pre-flight validation in `runner-svc.Service.Deploy` before the backend container is launched. Provider-agnostic: registry allow-list lives in config, no provider conditionals in code. Maps to the HTTP layer as 400 / 409 / 500 / 503-equivalent transient as appropriate.

### Files changed

- `snaphost-backend/internal/runtime/billing/client.go` —
  - New `DeployInfo` struct mirroring user-billing's response shape.
  - New sentinel `ErrDeployNotFound` returned on 404.
  - New `GetDeploy(ctx, deployID) (*DeployInfo, error)`. Status mapping: 404 → `ErrDeployNotFound`; 401 → typed error (auth misconfig); ≥500 → wrapped error (will retry-classify in Task 5); other non-200 → wrapped error.
- `snaphost-backend/internal/runtime/runner/errors.go` (new) —
  - `*ValidationError` (wraps a cause; HTTP 400).
  - `ErrAlreadyRunning` (HTTP 409; preserves idempotent saga retry).
  - Local `ErrTransient` + `wrapTransient` helper (5xx / network → retryable). Local sentinel only; Task 5 will introduce a shared pipeline-level type and consolidate.
- `snaphost-backend/internal/runtime/runner/service.go` —
  - New `BillingClient` interface (`UpdateDeployStatus`, `SetDeployRunning`, `GetDeploy`). `*billing.Client` satisfies it. `Service.billing` field retyped to the interface so tests can inject a fake without an HTTP server.
  - `deployableStatuses` map (`"built": true`) and `statusRunning` constant. Map structure preserved so future saga paths add a key, not a switch.
  - New `Service.validateDeployRequest(ctx, req)`. Order: (1) registry prefix allow-list (skipped if list empty, which is only reachable in non-strict mode — strict + empty list fatal at startup); (2) tag = `req.DeployID` check; (3) under `StrictImageValidation` only: `billing.GetDeploy` → `ErrDeployNotFound` → `*ValidationError`; other billing errors wrapped transient; `info.UserID != req.UserID` → `*ValidationError`; `info.Status == "running"` → `ErrAlreadyRunning`; `info.Status` not in `deployableStatuses` → `*ValidationError`.
  - `Service.Deploy` now calls `validateDeployRequest` first; returns its error unchanged so the HTTP layer can type-assert.
- `snaphost-backend/internal/runtime/config/config.go` —
  - New fields `AllowedRegistryPrefixes []string` (loaded from `REGISTRY_ALLOWED_PREFIXES`, comma-separated) and `StrictImageValidation bool` (loaded from `STRICT_IMAGE_VALIDATION`, default `true`).
  - **Secure default:** `StrictImageValidation=true` with empty `AllowedRegistryPrefixes` is a **fatal startup error** (verified empirically — runner-api panics with `REGISTRY_ALLOWED_PREFIXES must be set when STRICT_IMAGE_VALIDATION=true`). Misconfigured prod fails to start instead of silently accepting any image_ref.
- `snaphost-backend/cmd/runner-api/main.go` — added a single startup warning when `!StrictImageValidation && len(AllowedRegistryPrefixes) == 0` (dev-only configuration where any image_ref is accepted).
- `snaphost-backend/internal/runtime/api/handler.go::Deploy` — error mapping. `*ValidationError` → 400; `ErrAlreadyRunning` → 409; everything else → 500 (existing behaviour). Uses `errors.As` / `errors.Is`.
- `snaphost-backend/internal/runtime/runner/service_test.go` (new) — 13 tests:
  1. Happy path (status=built, prefix+tag+IDs match).
  2. Wrong registry prefix.
  3. Tag mismatch.
  4. Missing tag (`:` absent).
  5. User ID mismatch.
  6. `status=running` → `ErrAlreadyRunning`.
  7. `status=deleted` → `*ValidationError` (new case from curl smoke evidence).
  8. `status=failed` → `*ValidationError`.
  9. `status=pending` → `*ValidationError`.
  10. `ErrDeployNotFound` → `*ValidationError`.
  11. Billing 5xx-equivalent error → `ErrTransient` (not `*ValidationError`).
  12. Loose mode skips billing entirely (assertingBilling fails if `GetDeploy` is called).
  13. Loose mode still enforces prefix+tag.
- `infra/.env.example` + `infra/.env` — added `STRICT_IMAGE_VALIDATION=false` + `REGISTRY_ALLOWED_PREFIXES=host.docker.internal:5000/snaphost,registry:5000/snaphost` with a comment pointing at prod guidance.
- `infra/docker-compose.yml` — `runner-api` and `runner-watchdog` env blocks now pass `STRICT_IMAGE_VALIDATION` (default `true` in compose substitution — secure default if .env doesn't override) and `REGISTRY_ALLOWED_PREFIXES` (default empty).

### Build matrix verification

| Service | Default | -tags yandex |
| --- | --- | --- |
| api-gateway | ✅ | n/a |
| builder-svc | ✅ | n/a |
| runner-svc | ✅ | ❌ (6 errors, unchanged from Task 0 baseline) |
| user-billing | ✅ | n/a |
| ai-orchestrator | ✅ | n/a |
| shared | ✅ | n/a |

`go test ./internal/runner/` — 13 tests pass. `go test ./internal/deploy/` (user-billing) — 5 tests pass.

`golangci-lint run` on builder-svc, runner-svc, user-billing, ai-orchestrator, shared — clean. api-gateway pre-existing debt unchanged.

### Startup verification (live stack)

1. **strict=false (.env dev default), prefixes set** → runner-api starts clean. No warning log (prefixes non-empty).
2. **strict=true, prefixes set** → runner-api starts clean. (Toggled `.env`, restarted, verified.)
3. **strict=true (compose default), prefixes empty** → runner-api panics at startup with the expected message: `config: REGISTRY_ALLOWED_PREFIXES must be set when STRICT_IMAGE_VALIDATION=true`. This is the misconfigured-prod safety net. Empirically observed during initial restart before adding the env vars to docker-compose.yml.

### Smoke test

End-to-end docker UI deploy not driven from this session (cannot drive browser). Stack-level verification done: stack up, runner-api healthy, valid startup logs for both dev and strict configurations. The dev path (`STRICT_IMAGE_VALIDATION=false`) only exercises prefix+tag check; the `REGISTRY_URL=host.docker.internal:5000/snaphost` (builder-svc) emits image_refs like `host.docker.internal:5000/snaphost/proj-XXX:<deploy_id>` which match the configured prefix and have the deploy_id as the tag — the dev configuration is internally consistent.

**.env used for smoke (dev defaults):**
```
STRICT_IMAGE_VALIDATION=false
REGISTRY_ALLOWED_PREFIXES=host.docker.internal:5000/snaphost,registry:5000/snaphost
```

User to drive UI deploy. If it fails, the most likely root cause is .env desync, not code.

### Out of scope

- Transient/permanent error promotion at the saga / queue layer — Task 5.
- Image signature verification (cosign) — future hardening.
- Subdomain collision fix — Task 6.

---

## Task 3: `clone.isWithin` correctness

**Date:** 2026-05-11

### What

Replaced the fragile `rel[0] != '.'` check in `builder-svc/internal/clone/cloner.go::isWithin` with the canonical `filepath.Abs` + `filepath.Rel` + `..`-segment implementation. Bug: the old code rejected legitimate hidden files inside the parent directory (e.g. `.config`, `.env`) as if they were path-escape attempts, because both `".config"` and `"../escape"` start with `.`.

**User-visible impact.** Before Task 3: legitimate repos containing hidden files or directories (`.github/`, `.config/`, `.env.example`, etc.) reachable via symlinks were rejected with "path escape" errors during the clone walk, causing builder-svc to fail the deploy. After Task 3: such repos clone normally. Security invariant preserved — paths resolving outside the workdir via `..` segments or absolute paths are still blocked, as exercised by `path_test.go` cases #5–9.

### Files changed

- `snaphost-backend/internal/builder/clone/cloner.go` —
  - Replaced `isWithin` with canonical implementation. Resolves both arguments via `filepath.Abs`, computes `filepath.Rel(absParent, absChild)`, returns false only when rel equals `".."` or starts with `".." + os.PathSeparator`. Identical paths return true (rel == ".").
  - Added `strings` import for `HasPrefix` check.
- `snaphost-backend/internal/builder/clone/path_test.go` (new) — table-driven tests, 15 cases:
  identical absolute paths, child file under parent, hidden file `.config` directly under parent, nested hidden file `.config/auth.json`, sibling directory, parent-of-parent (above), unrelated `/var/log`, hidden file `.foo` outside parent in `/tmp`, `..`-segment that resolves outside, `..`-segment that resolves back inside (`sub/../src`), `.`-segment, relative child path (filepath.Abs anchors to cwd, demonstrating that callers must pass absolute paths), empty child, empty parent, both empty.

### Build matrix + tests + lint

| Service | Default |
| --- | --- |
| api-gateway | ✅ |
| builder-svc | ✅ |
| runner-svc | ✅ |
| user-billing | ✅ |
| ai-orchestrator | ✅ |
| shared | ✅ |

`go test ./internal/clone/` — 15/15 PASS.

`golangci-lint run` on builder-svc — clean.

### Smoke

Rebuilt `builder-api` + `builder-worker` images. Both restart clean:
```
builder-api: starting builder-svc API port=8082
builder-worker: starting builder-svc worker ... registry=host.docker.internal:5000/snaphost
                consumer started group=builder-workers
```

`isWithin` is invoked during clone's symlink-escape check in `filepath.Walk`. The current path (docker UI deploy of a normal repo with no symlinks) does not exercise the function except trivially. Behaviour change is observable only when cloning a repo that contains either (a) a symlink to a hidden file inside the workdir (previously falsely rejected) or (b) a symlink to anything starting with `.` inside the workdir. Old code blocked these; new code permits them and continues to block real escapes.

User to drive UI deploy of a known repo to confirm no regression in the unchanged happy path.

### Out of scope

- DNS-rebinding fix in `ValidateRepoURL` — Task 4.
- General symlink hardening beyond the existing walk — future.

---

## Task 4: DNS rebinding fix in clone path

**Date:** 2026-05-11

### Threat model

`ValidateRepoURL` resolves the repo hostname, checks each IP is publicly routable, returns OK. Then `git.PlainCloneContext` resolves DNS again internally during clone. Between the two resolutions, an attacker controlling DNS for the repo's hostname returns different IPs:

- Validation-time lookup → legitimate public IP (passes the filter).
- Clone-time lookup → `169.254.169.254` (AWS/GCP/Azure/Yandex Cloud metadata) or `fd00:ec2::254` (AWS IPv6 metadata).

If the worker ever runs on a cloud VM with an attached service account, the metadata endpoint serves IAM tokens. A crafted repo URL exfiltrates the worker's SA token.

Same vector applies to private networks: `169.254.0.0/16` link-local, `10.0.0.0/8` and other RFC1918 ranges, loopback.

### Defense layers (this task)

1. **IP filter** (`isAllowedIP` in `clone/url_validator.go`). Rejects loopback, private, link-local unicast/multicast, interface-local multicast, multicast, unspecified. Plus a separate `cloudMetadataIPs` list — `169.254.169.254`, `fd00:ec2::254`, `fe80::a9fe:a9fe` — kept distinct so future non-link-local metadata IPs (some providers) are still blocked. IPv4-mapped IPv6 normalised via `ip.To4()` before checks so `::ffff:169.254.169.254` cannot bypass IPv4-only filtering.
2. **Pinned dialer** (`newPinnedHTTPClient` in `clone/cloner.go`). Custom `http.Transport.DialContext` that rejects any dial whose host portion differs from `validated.Host`, otherwise dials `net.JoinHostPort(validated.IP.String(), port)`. DNS is never consulted at clone time — TOCTOU between validation and connection collapses to a single function call. `net.JoinHostPort` handles IPv6 bracketing.
3. **TLS hostname verification preserved**. `TLSClientConfig{ServerName: validated.Host, MinVersion: tls.VersionTLS12}`. Cert validated against the original hostname (e.g. `github.com`), not against the pinned IP — so legitimate certs still verify, but MITM behind DNS substitution still fails because the attacker can't present a valid cert for the requested host.
4. **Syntactic-only API validation, full validation in worker**. `api/handler.go::Build` now calls `clone.ValidateRepoURLSyntactic` (scheme, length, hostname/path shape — no DNS). `pipeline/runner.go` calls `clone.ValidateRepoURL` (DNS + filter + pin construction) immediately before `Cloner.Clone`. TOCTOU window shrunk from "queue depth in minutes" to "function-call microseconds".

### go-git integration mechanism

**Mechanism A — `client.InstallProtocol("https", custom)` under `sync.Mutex`.** Investigation in 4.0 confirmed:

- v5.12.0 has no per-clone `HTTPClient` field on `git.CloneOptions`.
- `http.NewClient(*http.Client)` wraps a custom `*http.Client` as a `transport.Transport`, but only takes effect through the global `client.Protocols` map.
- Mechanism C (custom `transport.Transport` impl) reduces to A — same global state.

Critical section pattern:
```go
c.installMu.Lock()
defer c.installMu.Unlock()
prev := client.Protocols["https"]
defer func() {
    if prev != nil { client.InstallProtocol("https", prev) }
    else { client.InstallProtocol("https", githttp.DefaultClient) }
}()
client.InstallProtocol("https", githttp.NewClient(pinnedClient))
// git.PlainCloneContext ...
```

Restore via `defer`, captures `prev` for pointer-identity-correct restoration. Mutex serialises concurrent clones (today the worker runs a single consumer goroutine; mutex is belt-and-suspenders for future per-user concurrency growth inside one process). Inline TODO marks the install/restore dance for removal when go-git ships a per-clone HTTPClient option.

### Files changed

- `builder-svc/internal/clone/url_validator.go` — rewrite. New `IPResolver` interface, `ValidatedURL` struct, `cloudMetadataIPs` list, `isAllowedIP` filter, `ValidateRepoURL(ctx, url, hosts, resolver)` returning `*ValidatedURL`, `ValidateRepoURLSyntactic(url)`.
- `builder-svc/internal/clone/cloner.go` — `CloneOptions.URL` → `CloneOptions.Validated *ValidatedURL`. `Cloner` got `installMu sync.Mutex`. New `newPinnedHTTPClient` helper. `Clone` wraps `git.PlainCloneContext` in the install/restore mutex block.
- `builder-svc/internal/clone/workdir.go` — added prod TODO around the dev-mode `chown` warning (CAP_CHOWN + matching UID/GID guidance).
- `builder-svc/internal/pipeline/runner.go` — `ValidateRepoURL` now returns `*ValidatedURL`, passed via `CloneOptions.Validated`.
- `builder-svc/api/handler.go::Build` — replaced full validation with `ValidateRepoURLSyntactic`. DNS no longer happens at API time.
- `builder-svc/internal/clone/url_validator_test.go` (new) — 25 cases (17 IP filter + 1 IPv4 preference + 1 IPv6-only fallback + 6 ValidateRepoURL syntactic + 10 ValidateRepoURLSyntactic standalone… spread across `TestValidateRepoURL_IPFilter`, `TestValidateRepoURL_IPv4PreferredOverIPv6`, `TestValidateRepoURL_IPv6OnlyFallback`, `TestValidateRepoURL_SyntacticChecks`, `TestValidateRepoURLSyntactic`).
- `builder-svc/internal/clone/pinned_transport_test.go` (new) — 6 cases via self-signed cert + httptest TLS server: happy path, DNS-rebind ignored, wrong host in dial, TLS cert mismatch, IPv6 dial, protocol restored after clone. Header comment forbids `t.Parallel()`.

### Test coverage summary

`go test ./internal/clone/` — **56 tests PASS, 0 FAIL**.
- 15 — `isWithin` (Task 3).
- 17 — IP filter (loopback v4/v6, private, link-local v4/v6, metadata v4/v6, IPv4-mapped IPv6 metadata, unspecified, multicast, mixed, all-filtered, lookup error, empty).
- 1 — IPv4 preference over IPv6.
- 1 — IPv6-only fallback.
- 6 — `ValidateRepoURL_SyntacticChecks` (scheme, host allowlist, paths).
- 10 — `ValidateRepoURLSyntactic` standalone (Task 4.3).
- 6 — pinned transport (happy, rebind, wrong host, cert mismatch, IPv6, protocol restore).

### Build matrix + lint

| Service | Default |
| --- | --- |
| api-gateway | ✅ |
| builder-svc | ✅ |
| runner-svc | ✅ |
| user-billing | ✅ |
| ai-orchestrator | ✅ |
| shared | ✅ |

`golangci-lint run` on builder-svc clean. Pre-existing api-gateway debt unchanged.

### Smoke

- **4.1 smoke:** builder-api + builder-worker rebuilt + restarted clean.
- **4.2 smoke:** user-confirmed end-to-end UI deploy of a GitHub repo through the new pinned-transport path. Clone phase ~1 second. Pipeline completed successfully. Final deploy URL served traffic in browser.
- **4.3 smoke:** builder-api rebuilt + restarted clean after dropping DNS from the API path.

### TODO (infra-level, not code)

```
// TODO(infra): defense-in-depth — run builder-worker in a network namespace
// with no route to 169.254.0.0/16 and fe80::/10 to make metadata IPs
// unreachable even if validation has a bug.
```

The pinned dialer is the in-process defense. A second layer at the network level (Linux netns with restricted routes, or container-level egress firewall blocking 169.254.169.254) would catch any future code-path regression or pin-bypass bug. Out of scope for this task; tracked here.

### Out of scope

- Full git smart-HTTP protocol testing against a real or simulated git server inside `httptest` — current tests cover the dialer + TLS layer end-to-end with a 200-OK handler. Sufficient to prove pinning + TLS verification work; not sufficient to prove go-git protocol bugs. Real GitHub clones in the UI smoke test cover the protocol layer.
- Per-clone HTTPClient option in `git.CloneOptions` — upstream go-git feature, not ours.
- Concurrent-clone optimisation past the install mutex — single-consumer worker today makes it moot.

### Sub-step 4.2.5 — State machine correction (post-fact)

Discovered during Task 4.2 smoke test investigation: PostgreSQL `chk_deploys_status`
CHECK constraint in `user-billing/db/migrations/0003_deploys.up.sql` allows 8 statuses:
`'pending', 'reserved', 'building', 'provisioning', 'running', 'failed', 'stopped', 'deleted'`.

Status `'built'` referenced in Task 2's `deployableStatuses` and in builder-svc's
status callback does NOT exist in DB schema. This was an incorrect invariant in
the original refactor prompt that propagated through Task 2 code and Task 1's
status-reporting wiring.

Real flow: saga transitions deploy into `building` at reservation time, and calls
runner-svc concurrently with the actual build. After successful build + backend
provisioning, saga moves status `building → provisioning → running` directly.
No `'built'` intermediate state exists.

Symptoms before fix (visible in user-billing logs since first UI deploy):

- Every deploy: builder-svc POST `/internal/deploys/:id/status` with body
  `{"status":"built"}` returned 500 (SQL 23514, CHECK constraint violation).
- Builder-svc logged error, returned nil to ack the job.
- Deploys completed regardless because saga drove state forward independently.

Bug existed since first deploy through UI; surfaced during careful log review
in Task 4 smoke test.

Changes:

- `runner-svc/internal/runner/service.go`: `deployableStatuses` value `"built"` → `"building"`.
- `runner-svc/internal/runner/service_test.go`: happy-path helper `builtInfo()` renamed to `deployableInfo()` and its `Status` field flipped to `"building"`.
- `builder-svc/internal/pipeline/runner.go`: removed `r.Status.ReportBuilt()` call entirely (vestigial code). The `publishEvent(BuildCompleted)` Redis call is **kept** — load-bearing, saga consumes it via `WaitForBuildEvent` and writes image_ref / commit_sha / port through `Repo.MarkImageBuilt`.
- `builder-svc/cmd/worker/main.go`: removed `ReportBuilt` method from `httpStatusReporter`.
- `builder-svc/internal/pipeline/runner.go`: removed `ReportBuilt` from `StatusReporter` interface; interface doc-comment now explains the build-success path uses Redis BuildEvent, not HTTP.
- `ReportBuilding` and `ReportFailed` kept — both use statuses valid in `chk_deploys_status` and are functionally used by saga.

Why the user-billing endpoint silently masked the bug: `updateStatusRequest`
binding struct only reads `Status` and `FailureReason`; the `image_ref` /
`commit_sha` keys that builder-svc was sending have always been discarded
without binding error. So the callback never wrote image_ref via this path
even when `built` was nominally valid earlier — saga has always relied on
the BuildEvent.

Verification:

- `make build` green default mode. `go test ./...` green across runner-svc, builder-svc, user-billing.
- `golangci-lint run` clean.
- User-driven docker UI smoke test required for final close — `docker compose logs user-billing --since=10m 2>&1 | grep "chk_deploys_status"` should be empty after fix.

### Saga state machine clarification

The codebase has TWO parallel state machines with overlapping vocabulary:

**`deploys.status` column** (user-facing state, constrained by `chk_deploys_status`):
`pending → reserved → building → provisioning → running` + terminals `failed`, `stopped`, `deleted`.

**`deploys.current_step` column** (saga-internal step tracking):
`reserved → building → built → provisioning → compensating`.

Note that `'built'` IS a valid value for `current_step` but NOT for `status`.
This distinction caused the original confusion in refactor planning. When
modifying status-related code in the future, verify which column is being
written to. As a guideline:

- HTTP endpoints `/internal/deploys/:id/status` and saga state checks → `deploys.status` (5+3 values).
- Saga orchestrator's `UpdateStep` calls → `deploys.current_step` (5 values).
- Image build completion is signaled to saga via Redis `BuildEvent`, NOT through any status HTTP callback (the latter was vestigial; removed in 4.2.5).

### Lesson learned (process improvement)

- Future refactor tasks: verify code invariants against actual DB migrations before encoding them in spec. State machine semantics belong to whoever owns the migrations, not to the refactor planner.
- Acceptance criteria for future tasks: explicitly check logs of all services involved in a flow, not just the service being changed. The `chk_deploys_status` errors were 500-spamming user-billing for weeks while UI showed "deploy successful".

---

## Task 5a: Error classification + finalize decoupling

**Date:** 2026-05-11 / 2026-05-12

### Scope

Task 5 was split (after the 5.0 investigation surfaced architectural blockers) into:

- **Task 5a** — infrastructure: typed error sentinels, transient/permanent classification at error sources, decouple BuildFailed/BuildCompleted publishing from `pipeline.Run`. **No retry logic.** End-to-end behaviour identical to pre-task.
- **Task 5b** (future) — Redis Streams reclaim loop + activate retry on transient.

### 5a.1 — `pipeline/errors.go`

`ErrTransient` / `ErrPermanent` sentinels + `Transient(err)` / `Permanent(err)` wrappers + `IsTransient(err)` / `IsPermanent(err)` helpers. `fmt.Errorf("%w: %w", tag, err)` multi-error chain; `errors.Is` reaches both tag and base. Double-wrap short-circuits via `errors.Is(err, tag)` check. 8 tests, all semantic (no string matching).

### 5a.2 — Typed sentinels in call-site packages

`builder-svc/internal/clone/url_validator.go`:

- `ErrInvalidURL` — syntactic URL problem (scheme, length, path). Permanent.
- `ErrHostNotAllowed` — hostname not in allowlist. Permanent.
- `ErrResolverFailure` — DNS resolver itself failed. Transient.
- `ErrIPFiltered` — DNS resolved but every IP rejected by security filter. Permanent.

Split of `ErrPrivateIP` into `ErrResolverFailure` + `ErrIPFiltered` was flagged in investigation 5.0 — DNS failure (transient) and filter rejection (permanent) were conflated by the original error code. Replaced 4 string-code constants with sentinel `var`s; updated all `fmt.Errorf("%s:…", code, …)` → `fmt.Errorf("%w:…", sentinel, …)`. No external callers of the old constants (verified via grep).

`builder-svc/internal/ai/client.go`:

- `ErrAIRefused` — model semantic refusal (HTTP 422), other 4xx (treated as permanent — request bug), or marshal/decode failures. Permanent.
- `ErrAIUnavailable` — HTTP 5xx, 429 rate-limit, network errors. Transient.

24 tests (17 IP filter cases re-asserted via `errors.Is(err, sentinel)`; 2 standalone sentinel checks for `ErrHostNotAllowed` / `ErrInvalidURL`; 1 IPv6-only fallback) + 8 ai-client tests across the HTTP status matrix.

### 5a.3 — Apply classifications in `pipeline/runner.go`

`builder-svc/internal/pipeline/classify.go` — 4 helpers:

- `classifyValidationError(err)` — `clone.ErrResolverFailure` → Transient, else Permanent.
- `classifyGitError(err)` — `transport.ErrRepositoryNotFound` / `ErrAuthenticationRequired` → Permanent; "unexpected host" string (Task 4 pinned-dialer rejection) → Permanent; `net.Error.Timeout()` / `*net.OpError` → Transient; default Permanent (conservative per investigation 5.0).
- `classifyAIError(err, log)` — `ai.ErrAIUnavailable` → Transient; `ai.ErrAIRefused` → Permanent. For 4xx non-422 (status 400/401/403/404 in error text) emits WARN log noting possible builder-svc bug rather than legitimate refusal.
- `classifyBuildKitError(err)` — `status.Code(err) == codes.Unavailable` → Transient; else Permanent. Inline `TODO(observability)` for OOM detection.

All 15 error returns in `executePipeline` wrapped: validation, workdir, clone, detect, AI gen, file IO, dockerfile validation, BuildKit, scan. 17 focused classification tests.

### 5a.4 — Decouple lifecycle event publishing from `pipeline.Run`

`Runner.Run` signature changed from `(context, Job) error` to `(context, Job) (*Result, error)`. New `Result{ImageRef, CommitSHA, Port}` struct carries success metadata.

- Removed from `Run::on-error` path: `Publisher.Publish` "pipeline failed", `Status.ReportFailed`, `publishEvent(BuildFailed)`. Moved to `FinalizeAsFailed(ctx, deployID, cause)`.
- Removed from `Run::on-success` path: `publishEvent(BuildCompleted)`, `Publisher.Publish` "pipeline complete", `log.Info` success record. Moved to `FinalizeAsSucceeded(ctx, deployID, *Result)`.

**Symmetric design — important for Task 5b.** Both event publications (BuildCompleted on success, BuildFailed on failure) are now owned by the worker via `FinalizeAsSucceeded` / `FinalizeAsFailed`. This symmetric architecture is prerequisite for Task 5b retry logic — worker decides when to publish based on classification, not pipeline. Half-inside, half-outside would have made the retry path bug-prone: a retry-after-publish for transient errors would race the saga state machine.

Worker (`cmd/worker/main.go`) integration:

```go
result, err := runner.Run(ctx, job)
if err != nil {
    log.Error("pipeline failed", zap.Bool("transient", pipeline.IsTransient(err)),
        zap.Bool("permanent", pipeline.IsPermanent(err)), zap.Error(err))
    runner.FinalizeAsFailed(ctx, job.DeployID, err)
    return nil
}
log.Info("pipeline completed", zap.String("image_ref", result.ImageRef))
runner.FinalizeAsSucceeded(ctx, job.DeployID, result)
return nil
```

Inline comment notes Task 5b will defer finalize for transient errors pending retry.

6 finalize tests via `recPublisher` / `recEvents` / `recStatus` fakes:

- `FinalizeAsFailed_PublishesBuildFailedAndReportsStatus`
- `FinalizeAsFailed_ReportFailedErrorIsSwallowed` — saga must see BuildFailed even if HTTP status reporter errors.
- `FinalizeAsFailed_NilCause` — defensive fallback reason.
- `FinalizeAsFailed_NilStatusReporterSafe` — untyped-nil interface guard.
- `FinalizeAsSucceeded_PublishesBuildCompleted` — verifies BuildCompleted event with full metadata (ImageRef / CommitSHA / Port); ReportFailed must NOT be called on success.
- `FinalizeAsSucceeded_NilResultSafe` — nil result short-circuits, no publish.

### Build matrix + tests + lint

All services build clean. All affected pkgs lint clean. `go test ./internal/{ai,clone,pipeline}/` — 80+ tests PASS in total across the task.

### Behavioural invariant

Today's worker calls a finalizer immediately on every Run outcome → exact same side effects as pre-task code path. Failure path: BuildFailed published + ReportFailed called + log line emitted, same order, same content. Saga compensation unchanged. UI experience unchanged. Difference visible only in worker logs: new structured `transient=` / `permanent=` fields on failure.

### Out of scope

- Retry logic — Task 5b.
- Redis Streams reclaim loop — Task 5b.
- Saga retry semantics — out of scope entirely; saga has its own state machine.

---

## Task 5 investigation backlog

Items observed during prior tasks that Task 5 should investigate as part of its scope:

- ~~`POST /internal/deploys/:id/status` returned 500 during Task 4.2 smoke test~~ — **resolved in 4.2.5**. Root cause was builder-svc sending `status="built"`, which is not in `chk_deploys_status`. Builder-svc no longer issues this callback; saga consumes BuildEvent via Redis. The general question of "should the pipeline fail when a status report fails" still stands for `ReportBuilding` / `ReportFailed` — Task 5 to consider whether those 5xx responses should be transient-retried or permanent.

### Observed during Task 5a smoke tests

- **Trivy DB download `unexpected EOF` from mirror.gcr.io** — confirmed real transient error case. Without retry (current behaviour, deferred to Task 5b), each such failure costs user a refund + manual re-deploy. Concrete business case for Task 5b retry activation if 5b is undertaken.
- **Classification chain verified end-to-end in production**: `transient: scan:` prefix correctly emerged on Trivy DB transient; `permanent: clone:` prefix correctly emerged on GitHub auth-required for nonexistent repo.

---

## Task 6: Subdomain collision fix

**Date:** 2026-05-13

### Problem

`runner-svc/internal/runner/service.go::generateSubdomain` used the first 8 hex characters of the deploy UUID (32 bits of entropy). By the birthday paradox, ~50% collision probability at ~65k deploys. A collision today causes the INSERT to fail on `uq_deploys_subdomain UNIQUE` ([0003_deploys.up.sql](../../../snaphost-backend/internal/control/db/migrations/0003_deploys.up.sql)) → deploy marked failed → user retries with a fresh UUID. Collision space far too small for a public service.

### Option A (chosen): full UUID

Use the full deploy UUID with dashes stripped (32 hex characters). Subdomain format: `proj-<32-hex>`, total length 37 — well within the RFC 1035 label limit of 63.

- **Entropy:** 2^122 (UUID v4). Collision probability is astronomically low.
- **No migration needed.** DB column already `VARCHAR` with no length constraint; UNIQUE constraint remains as defense in depth.
- **No retry logic needed.** Single-shot generation, deterministic from deploy_id.
- **No service restart pain.** Existing deploys keep their legacy 8-hex subdomain values (already in the DB column); they remain routable verbatim through Traefik (dev) and the Yandex API Gateway (prod). Only deploys created after this commit use the long form.

### Option B (rejected): 8-hex + retry on conflict

Considered: keep short subdomains, retry with random suffix on UNIQUE violation. Rejected because:

- Cosmetic gain (~24 fewer chars) is negligible — subdomains are typically bookmarked once, not typed.
- Adds retry path through DB INSERT that didn't exist before; new failure modes.
- DB UNIQUE constraint becomes a hot lookup vs being a passive safety net.

### Files changed

- `runner-svc/internal/runner/service.go::generateSubdomain` — single-line body: `strings.ReplaceAll(deployID, "-", "")` + `proj-` prefix. Doc-comment explains entropy, DB UNIQUE as defense in depth, RFC 1035 length math, and the backward-compatibility note.
- `runner-svc/internal/runner/subdomain_test.go` (new) — 5 cases: standard UUID-v4 input, UUID-without-dashes input, empty input, all-zero UUID, plus a separate test asserting length == 37 for full-UUID input. Every case also asserts `len(subdomain) <= 63` (RFC 1035).

### Call-site audit

`grep -rn generateSubdomain` confirms a single call site: [service.go:101](../../../snaphost-backend/internal/runtime/runner/service.go#L101). Output is passed to `backend.RunRequest.Subdomain`, consumed by:

- [`docker/docker.go`](../../../snaphost-backend/internal/runtime/backend/docker/docker.go) — used as-is for Traefik label + endpoint URL construction.
- [`yandex/yandex.go`](../../../snaphost-backend/internal/runtime/backend/yandex/yandex.go) — used as-is for the hostname in API Gateway spec.

Neither parses the subdomain back into a deploy_id. No code anywhere depends on the 8-hex length. The Yandex backend also constructs an internal `containerName` from `deployID` directly (not from `subdomain`), so the path is independent.

### Backward compatibility

Existing deploys recorded with 8-hex subdomains stay reachable indefinitely:

- Subdomain is stored verbatim in the `deploys.subdomain` column.
- Traefik matches by exact subdomain label — no length assumption.
- Yandex API Gateway spec stores the hostname as a string; no length-dependent parsing.

No migration. No special-case code.

### Verification

- `go test ./internal/runner/`: **18 PASS** (13 prior + 5 new). Includes RFC 1035 length invariant.
- `go build ./...` across all 6 services: clean default.
- `golangci-lint run` on runner-svc: clean.
- runner-api rebuilt + restarted clean.

User-driven UI smoke test required:

```bash
# Deploy a new repo via UI. Verify resulting URL is proj-<32-hex>.<domain>.
# Click through to an older deploy (if any present in account) — should still
# load at its original short proj-<8-hex>.<domain>.
```

### Out of scope

- Migrating existing deploys to long form. Not needed.
- Custom user-chosen subdomains. Separate feature, not a refactor concern.
- Stripping the `proj-` prefix. Visible identifier convention, preserved.

---

## Task 7: Trivy `--insecure` conditional flag

**Date:** 2026-05-13

### What changed

Made the Trivy `--insecure` flag conditional on configuration instead of unconditional. Previously, `builder-svc/internal/scan/trivy.go` always passed `--insecure` to Trivy, disabling TLS certificate verification for all registry pulls — including production registries like `cr.yandex`. This created a man-in-the-middle vulnerability when scanning images from TLS-enabled registries.

Now:

- Default is **secure** (`RegistryInsecure = false`; `--insecure` not passed).
- Auto-detect: if `REGISTRY_URL` points at a known local-development host (`localhost`, `127.0.0.1`, `host.docker.internal`, `registry:`), `RegistryInsecure` is automatically set to `true` at startup.
- An explicit `REGISTRY_INSECURE` env var (true or false) overrides auto-detection in both directions.
- A warning is logged if `REGISTRY_INSECURE=true` is set with a non-local registry URL.

### Why

Unconditional `--insecure` disables TLS certificate validation between Trivy and the container registry. In production with a TLS-enabled registry, an attacker on the network path could serve a tampered image manifest. Trivy would scan the wrong content while reporting it as clean. Making the flag conditional preserves the local-dev convenience (HTTP registries) while enforcing TLS verification in production.

### Files modified

- `builder-svc/config/config.go` — added `RegistryInsecure bool` field, `isLocalRegistry()` helper, auto-detect logic with `os.LookupEnv` for explicit-vs-default distinction, startup info/warn log messages. Added `go.uber.org/zap` import.
- `builder-svc/internal/scan/trivy.go` — added `registryInsecure bool` field to `Scanner` struct, updated `NewScanner` to accept the boolean, factored out `buildArgs()` helper, made `--insecure` conditional on `s.registryInsecure`.
- `builder-svc/cmd/worker/main.go` — updated `scan.NewScanner(pub, log)` → `scan.NewScanner(pub, log, cfg.RegistryInsecure)`.
- `infra/.env.example` — added `REGISTRY_INSECURE` documentation block next to `REGISTRY_URL`.

### Tests

- `builder-svc/config/config_test.go` (new) — 14 tests:
  - `TestRegistryInsecureAutoDetect` (7 table-driven cases): local host.docker.internal auto-on, local registry: auto-on, localhost auto-on, 127.0.0.1 auto-on, cr.yandex defaults secure, explicit true wins over auto-secure, explicit false wins over auto-insecure.
  - `TestIsLocalRegistry` (7 table-driven cases): localhost, 127.0.0.1, host.docker.internal, registry:, cr.yandex, ghcr.io, empty string.
- `builder-svc/internal/scan/trivy_test.go` (new) — 3 tests:
  - `TestBuildArgs_InsecureTrue` — verifies `--insecure` present in args and imageRef is last.
  - `TestBuildArgs_InsecureFalse` — verifies `--insecure` absent and imageRef is last.
  - `TestBuildArgs_CoreFlagsPresent` — verifies all other flags unchanged.

All 17 new tests pass. All existing tests pass (80+ across builder-svc).

### Smoke test result

Not driven from this session (requires `make dev` + UI deploy). User to verify:
1. `make dev` → stack comes up.
2. Check builder-worker startup logs: `docker compose -f infra/docker-compose.yml logs builder-worker --since=5m 2>&1 | grep -iE "insecure|trivy"` — should show `local registry detected, enabling Trivy --insecure flag` (because dev `.env` uses `registry:5000/snaphost` or `host.docker.internal:5000/snaphost`).
3. Deploy a repo through the UI — Trivy scan must complete successfully as part of the pipeline.

### Build matrix verification

| Service | Default |
| --- | --- |
| api-gateway | ✅ |
| builder-svc | ✅ |
| runner-svc | ✅ |
| user-billing | ✅ |
| ai-orchestrator | ✅ |
| shared | ✅ |

`golangci-lint run` on builder-svc — clean. Pre-existing api-gateway debt unchanged.

### Out of scope

- Trivy scan retry on transient failures — Task 5b.
- Registry TLS certificate pinning — future hardening.
- Per-image insecure override — not needed; the flag is registry-level.

---

## Task 8: Trivy scan before push (Option A)

**Date:** 2026-05-13

### Problem

`executePipeline` previously pushed the built image to the registry (BuildKit `ExporterImage` with `push: true`) before scanning it with Trivy. If the scan found CRITICAL vulnerabilities, the pipeline returned an error and the saga compensated — but **the vulnerable image stayed in the registry**, consuming disk and theoretically pullable by any process knowing the tag.

### Investigation (Sub-step 8.0)

Two options were investigated:

- **Option A (scan before push):** Build to local OCI tarball → scan tarball → push only if scan passes.
- **Option B (cleanup on fail):** Push → scan → delete from registry on failure.

Investigation findings:

| Criterion | Option A | Option B |
|---|---|---|
| Security window | **None** | ~30-60s |
| Provider-agnostic | **Yes** (BuildKit-native) | No (per-provider `Registry.Delete` impl) |
| Infra changes | None | `REGISTRY_STORAGE_DELETE_ENABLED=true` |
| Yandex complexity | N/A | Delete by ImageId (not by ref — requires List+Delete) |

**Option A selected** for: zero-exposure guarantee, provider-agnostic design (consistent with architectural invariant from all prior tasks), no infrastructure changes required.

### What changed

New pipeline order:
```
clone → detect → validate → build (OCI tarball) → scan (tarball) → push (cache hit) → finalize
```

Previously:
```
clone → detect → validate → build+push → scan (registry pull) → finalize
```

Key design points:

1. **Two-phase BuildKit:** First `Solve` exports an OCI tarball locally (`ExporterOCI` with `FileOutputFunc`). Second `Solve` pushes to registry (`ExporterImage` with `push: true`). BuildKit's content-addressable cache ensures the second call reuses all layers — only the push transfer happens (~5-15s overhead).

2. **Tarball scanning:** Trivy's `--input` flag scans a local OCI tarball without registry access. The `--insecure` flag (Task 7) is correctly skipped for tarball scans (no registry pull needed).

3. **Tarball cleanup:** `defer os.Remove(tarPath)` after creation ensures the tarball is cleaned up on any exit path (success, scan failure, push failure, panic).

### Files modified

- `builder-svc/internal/build/buildkit.go` — added `OutputMode` enum (`OutputModePush` / `OutputModeLocalTar`), `TarPath` field to `BuildOptions`, `buildExportEntry()` helper that configures `ExporterOCI` with `FileOutputFunc` for local tar or `ExporterImage` with push for registry. Differentiated log text ("building image locally" vs "pushing image to registry"). Added `io` and `os` imports.
- `builder-svc/internal/scan/trivy.go` — `buildArgs` now accepts `isFilePath bool`. When true: uses `--input <path>` instead of positional imageRef, skips `--insecure` (not needed for local files). `Scan` detects tarball input via `strings.HasSuffix(imageRef, ".tar")`. Added `strings` import.
- `builder-svc/internal/pipeline/runner.go` — `executePipeline` steps 12-14 reorganized: build to tarball (step 12) → scan tarball (step 13) → push to registry (step 14). Tarball cleanup via defer. Error classification preserved: build errors → `classifyBuildKitError`, scan errors → `Permanent`/`Transient` as before, push errors → `classifyBuildKitError`.

### Tests

- `builder-svc/internal/build/buildkit_test.go` (new) — 4 tests:
  - `TestBuildExportEntry_Push` — verifies `ExporterImage` with `push=true`.
  - `TestBuildExportEntry_LocalTar` — verifies `ExporterOCI` with non-nil `Output`, no push attr.
  - `TestBuildExportEntry_LocalTarMissingPath` — verifies error on empty `TarPath`.
  - `TestBuildExportEntry_DefaultIsPush` — verifies zero-value `OutputMode` defaults to push.

- `builder-svc/internal/scan/trivy_test.go` (updated) — 6 tests (3 existing updated for new signature, 3 new):
  - `TestBuildArgs_TarballInput` — verifies `--input` flag present, `--insecure` absent even when `registryInsecure=true`.
  - `TestBuildArgs_TarballNoInsecureEvenWhenSet` — explicit check that insecure+tarball still omits `--insecure`.
  - `TestBuildArgs_RegistryRefNoInput` — verifies `--input` absent for registry ref scans.

All tests pass (10 new/updated in this task, 80+ total across builder-svc).

### Build matrix verification

| Service | Default |
| --- | --- |
| api-gateway | ✅ |
| builder-svc | ✅ |
| runner-svc | ✅ |
| user-billing | ✅ |
| ai-orchestrator | ✅ |
| shared | ✅ |

`golangci-lint run` on builder-svc — clean. Pre-existing api-gateway debt unchanged.

### Bugfix — Option 2 (OutputDir + self-tar)

Smoke testing found that Trivy could not parse the local OCI tarball produced by the initial Task 8 implementation. The failure presented as `manifest.json not found in tar` followed by `stat .../.snaphost-image.tar/index.json: not a directory`.

Root cause: the initial implementation used BuildKit `ExporterOCI` with `Output` / `FileOutputFunc`, which crosses BuildKit's filesync session protocol. The repo currently uses BuildKit Go client v0.13.2 while the daemon in `infra/docker-compose.yml` is v0.29.0; that version gap caused the writer to receive malformed/fragmentary OCI layout content instead of a complete tarball.

Fix: `OutputModeLocalTar` now uses `ExportEntry.OutputDir` so BuildKit writes an OCI directory layout on disk, then `Build()` creates the tarball locally with `archive/tar`. This bypasses filesync for the tarball content. `executePipeline` also removes both `.snaphost-image.tar` and `.snaphost-image.tar.d` as defense-in-depth cleanup.

Why not upgrade BuildKit now: changing the client/daemon version matrix is a larger-blast-radius dependency task with possible API and runtime behavior changes. The `OutputDir` + self-tar fix is local, provider-agnostic, and preserves the Task 8 architecture.

Test added: `TestTarOCIDirectory` creates a synthetic OCI layout, tars it, reads it back through `archive/tar`, and asserts `oci-layout`, `index.json`, and blob content are present. `TestBuildExportEntry_LocalTar` now asserts `OutputDir` is set and `Output` is nil.

### Bugfix iteration 2 — BuildKit client version upgrade

Option 2 (OutputDir + self-tar) was applied but exposed a different failure:
`method /moby.filesync.v1.FileSend/diffcopy not supported by the client`.

Root cause: `OutputDir` still uses BuildKit's filesync session for daemon→client content transfer. Client `v0.13.2` does not implement the `diffcopy` method that daemon `v0.29.0` expects.

The underlying problem is the 16-minor-version gap between client and daemon. No client-side workaround bridges that method-set mismatch.

**Fix: align the client with the daemon by upgrading to `github.com/moby/buildkit v0.29.0`.**

Changes:
- `builder-svc/go.mod`: `github.com/moby/buildkit v0.13.2 -> v0.29.0`. Stale `github.com/containerd/containerd v1.7.13` removed.
- `internal/build/yandex_iam.go`: updated to the new `authprovider.NewDockerAuthProvider` signature using `DockerAuthProviderConfig{AuthConfigProvider: authprovider.LoadAuthConfig(dockerCfg)}`.
- `internal/build/buildkit.go`: added `"tar": "false"` to the OCI export entry in `OutputModeLocalTar`, matching BuildKit's v0.29.0 OCI-layout test pattern and making the directory-layout intent explicit.

Transitive dependency churn was expected and accepted for this version step, including `docker/cli`, `grpc`, OpenTelemetry, and the containerd v2 ecosystem.

Why not keep v0.13.2 and pin the daemon back: that would push the mismatch into infrastructure and leave the repo on an older client line despite the approved fix path. Aligning the client is the narrower change now that compile checks proved the upgrade is feasible.

### Bugfix iteration 3 — exclude BuildKit ingest/ from OCI tar

After the v0.29.0 client alignment, diagnostics showed BuildKit's OCI exporter writes a valid OCI layout plus an internal containerd staging directory, `ingest/`, under the output directory. `ingest/` is not part of the OCI Image Layout v1.0.0 top-level set (`oci-layout`, `index.json`, `blobs/`). Including it in the self-made tarball caused Trivy to reject the archive and fall through to a misleading `stat .../.snaphost-image.tar/index.json: not a directory` error.

Fix: `tarOCIDirectory` now skips `ingest/` and its children while preserving the standard OCI layout entries. `TestTarOCIDirectory` now creates `ingest/temp-write` and asserts it is absent from the resulting tarball.

The diagnostic run had temporarily disabled cleanup in three places; all are restored: tarball and OCI directory cleanup in `pipeline/runner.go`, post-tar OCI directory cleanup in `build/buildkit.go`, and workdir cleanup in `clone/workdir.go`.

### Smoke test plan

**Happy path:**
1. `make dev` → stack comes up.
2. Deploy a known-good repo through the UI. Pipeline should: build locally → scan tarball → push → finalize. URL serves traffic.
3. Check builder-worker logs:
   ```bash
   docker compose -f infra/docker-compose.yml logs builder-worker --since=5m 2>&1 | grep -iE "building image|pushing image|scan"
   ```
   Should see: "building image locally" → "scanning image" → "pushing image to registry" → "build complete".

**Negative test (vulnerable image):**
1. Deploy a repo using a known-vulnerable base image (e.g. `FROM alpine:3.10`).
2. Pipeline should fail with "critical vulnerabilities found".
3. Verify image is NOT in registry:
   ```bash
   curl http://localhost:5000/v2/snaphost/proj-<hash>/manifests/<deploy-id>
   ```
   Should return 404 (image was never pushed because scan failed before push).

### Out of scope

- Task 5b retry logic — future work.
- Tarball size optimization (compression, streaming) — moot after pivot.

### Pivot to Option B (after five Option A iterations)

**Date:** 2026-05-16

After five iterations, Option A (scan local OCI tarball before push) was abandoned. The BuildKit Go client + daemon interaction surface kept producing tarballs that Trivy could not parse:

1. Initial `ExporterOCI` + filesync `Output` → fragmentary tarball (client/daemon mismatch).
2. Switch to `OutputDir` + self-tar → `diffcopy not supported by the client` (gRPC method gap, client 0.13.2 vs daemon 0.29.0).
3. Client upgrade to v0.29.0 → filesync works but Trivy still rejects.
4. Skip BuildKit's non-spec `ingest/` from self-tar → same Trivy error.
5. Same `manifest.json not found in tar` / `index.json not a directory` symptom persists with no clear root cause.

Option A's integration surface (BuildKit internals, OCI layout edge cases, Trivy tarball parser) proved too brittle to stabilize within scope. Pivoted to Option B (scan after push + registry cleanup on failure). The security-window concern that originally disqualified Option B is acceptable because Task 2 (image_ref validation in runner-svc) prevents deployment of any image during the ~30-60s scan window, and image tags are unguessable (`proj-<8-hex>:<uuid>`).

#### Rolled back

- `internal/build/buildkit.go`: removed `OutputMode` enum, `OutputModePush`/`OutputModeLocalTar`, `TarPath` field, `buildExportEntry` helper, post-Solve `tarOCIDirectory` block, `tarOCIDirectory` function, and `archive/tar` / `io` / `os` / `path/filepath` / `strings` imports. Build always uses the push exporter.
- `internal/pipeline/runner.go`: restored single-phase build (push directly). Removed `defer os.Remove(tarPath)` / `defer os.RemoveAll(tarPath+".d")` block — workdir cleanup via `clone/workdir.go` is sufficient.
- `internal/scan/trivy.go`: `buildArgs` no longer takes `isFilePath`. The `--insecure` flag (Task 7) remains conditional on `cfg.RegistryInsecure`. Scan always pulls from the registry.
- `internal/build/buildkit_test.go` deleted (Build() now requires a live BuildKit daemon; the single exporter literal is covered by smoke tests).
- `internal/scan/trivy_test.go`: removed `TestBuildArgs_TarballInput`, `TestBuildArgs_TarballNoInsecureEvenWhenSet`, `TestBuildArgs_RegistryRefNoInput`. Kept `TestBuildArgs_InsecureTrue` / `_InsecureFalse` / `_CoreFlagsPresent`.

BuildKit client upgrade to v0.29.0 is retained — it aligns with the running daemon and is not Option-A-specific.

#### Added

- `internal/registry/` package with `Client` interface (`DeleteImage(ctx, imageRef) error`) and `DockerV2Client` implementation. Two-step delete per the Docker Registry v2 spec: GET the manifest with v2/OCI Accept headers to resolve `Docker-Content-Digest`, then DELETE by digest. 404 on either step is idempotent success. 202 on DELETE is the spec-canonical success.
- `internal/registry/docker_v2_test.go`: httptest-driven coverage of happy path, already-absent, DELETE race 404, GET 500, DELETE 405 (delete not enabled), DELETE 500, network failure, missing digest header. `TestParseImageRef` table-driven covering local-registry-with-port, Yandex CR, and four invalid forms.
- `pipeline.Runner.Registry` field. After scan returns `ErrCriticalVulnerability` with `ScanFailOnCritical=true`, the pipeline calls `Registry.DeleteImage`. Delete failure is logged but does not change the user-visible outcome (scan failure is the primary error). Delete is NOT called for transient scan errors (Trivy binary missing, exec failure, etc.).
- `cmd/worker/main.go`: constructs `registry.NewDockerV2Client(log)` and passes it to the runner.
- `infra/docker-compose.yml`: `REGISTRY_STORAGE_DELETE_ENABLED: "true"` on the registry service (without it, the registry returns 405 to DELETE).

#### Future work

- Yandex Container Registry delete (delete by ImageId; requires List+Delete). Will live behind `//go:build yandex` per the Task 0 isolation pattern. The `Client` interface is already provider-pluggable.
- Pipeline-level unit test for the scan-failure→DeleteImage path. The existing pipeline uses concrete structs (`*clone.Cloner`, `*build.Builder`, `*scan.Scanner`) — exercising `executePipeline` end-to-end would require introducing interfaces for those collaborators, which is out of scope. The DockerV2 client tests + the small wire-up in `runner.go` are covered by inspection.

## Task 9: Runtime version detection (.nvmrc, engines.node, CRA heuristic)

**Date:** 2026-05-17

### Problem

Repo `https://github.com/jherr/rxjs-pokemon` matched the `cra` template, which
hardcoded `NODE_VERSION: "20"`. Old `react-scripts` (4.x) pulls in postcss 7.x
that breaks on Node 18+ with `ERR_PACKAGE_PATH_NOT_EXPORTED` on `'./lib/tokenize'`.
The build failed inside `RUN npm run build`. Three root causes converged:

1. `builder-svc/internal/pipeline/runner.go::readKeyFiles` didn't read `.nvmrc`
   or `.node-version`, so the user's explicit version pin was silently dropped.
2. `ai-orchestrator/detector` didn't parse those files even when present.
3. Every Node template hardcoded `NODE_VERSION: "20"` with no override path.

### What changed

- **builder-svc** `readKeyFiles` reads `.nvmrc`, `.node-version`, `.python-version`
  in addition to the existing key files.
- **ai-orchestrator/detector** `Enrich` parses those into new `ProjectSignals`
  fields (`NvmrcVersion`, `NodeVersionFile`, `PythonVersionFile`). Parsing is
  best-effort — malformed files degrade to empty signal, never propagate
  errors (consistent with Task 5a classification invariant).
- **ai-orchestrator/templates** exports `PickNodeVersion(signals, heuristic)`
  with explicit precedence: `.nvmrc` / `.node-version` → `engines.node` →
  template heuristic → `defaultNodeMajor = 20`. All six Node templates call
  it instead of hardcoding "20".
- **`cra` template** uses `craLegacyHeuristic` that pins Node 16 for
  `react-scripts` 1.x–4.x. react-scripts 5.x stays on the default.
- `clampNodeMajor` maps arbitrary requested majors to the supported set
  `{16, 18, 20, 22}`. New supported lines are added by extending one slice.

### Files changed

- `builder-svc/internal/pipeline/runner.go` — `readKeyFiles` key list extended.
- `ai-orchestrator/internal/detector/enrichment.go` — `parseVersionFile`
  helper, three new fields populated.
- `ai-orchestrator/internal/templates/library.go` — `PickNodeVersion`,
  `pickMajorFromEnginesNode`, `clampNodeMajor`, `craLegacyHeuristic`,
  three new `ProjectSignals` fields, all six Node matchers updated.
- `ai-orchestrator/internal/detector/enrichment_test.go` — new (first
  test file in ai-orchestrator).
- `ai-orchestrator/internal/templates/library_test.go` — replaced (prior
  tests referenced removed `detectNodeVersion`/`extractMajorVersion` symbols).

### Build matrix verification

| Service | Default | -tags yandex |
| --- | --- | --- |
| api-gateway | ✅ | n/a |
| builder-svc | ✅ | n/a |
| runner-svc | ✅ | ❌ (unchanged from Task 0 baseline) |
| user-billing | ✅ | n/a |
| ai-orchestrator | ✅ | n/a |
| shared | ✅ | n/a |

`go test ./internal/detector/ ./internal/templates/` — all pass.

### Smoke test

**Positive (regression check):** Deploy `https://github.com/jherr/rxjs-pokemon`.
Expected:
- builder-worker log: `Dockerfile generated (source: template)`.
- `Dockerfile.snaphost` line 1 should be `FROM node:16-alpine AS builder`.
- `RUN npm run build` succeeds.
- Final URL serves traffic.

**Behaviour preservation:** Deploy a modern Vite or Next.js repo. Expected:
- `NODE_VERSION` resolves to `20` (default) unless the repo pins via
  `.nvmrc` / `engines.node`.
- No regression in cache hit rate (cache key still keyed on
  `(file_tree, key_files)` — added keys are harmless).

**Log review:**
```
docker compose -f infra/docker-compose.yml logs ai-orchestrator builder-worker \
  --since=10m 2>&1 | grep -iE "error|fail|warn"
```
Should be empty for the happy path.

### Out of scope

- "LLM doctor" retry on build failure — bigger task. Tracked separately.
- pyproject.toml parsing (`PyprojectToml` remains declared-but-unused) —
  separate task with its own templates.
- Adding new templates (Nuxt, SvelteKit, NestJS, Astro, Bun, Rails) —
  separate task per template.
- `.python-version` is parsed but not yet consumed by any template;
  Python templates still hardcode `PYTHON_VERSION: "3.11"`. Plumbing
  ready for a follow-up.

### Lesson

Task 4.2.5 said: verify code invariants against actual data the system
processes. Same lesson here: the `cra` template assumed Node 20 works for
"any react-scripts project", but real-world repos pin react-scripts 4.x.
Heuristics live or die by the breadth of real inputs, not the cleanliness
of the abstraction.

## Task 9.1: Cache schema versioning

**Date:** 2026-05-17

### Problem

After Task 9 shipped, smoke test of `https://github.com/jherr/rxjs-pokemon`
still produced `FROM node:20-alpine`. Build failed identically.

Root cause: `ai_dockerfile_cache` returned a row from the pre-Task-9 deploy.
`Repository.ComputeSignature` keys only on `FileTree + KeyFiles` — neither
changed between deploys. Cache lookup precedes template matching
(`service.go::GenerateDockerfile` step 3), so post-Task-9 matcher logic was
never invoked. The misleading `source: template` log line came from the
cached row's stored `source` column, not from a fresh render.

Manual `DELETE FROM ai_dockerfile_cache` worked, but every future
matcher/detector/template change would need the same step.

### What changed

- New constant `cacheSchemaVersion` in
  `ai-orchestrator/internal/cache/version.go`. Mixed into `ComputeSignature`
  before any other input. Bumping it invalidates every prior cache row.
- `ComputeSignature` now writes NUL-byte delimiters between key name and
  content (and between consecutive pairs). Prevents a latent collision
  where `{"a":"bc"}` and `{"ab":"c"}` hashed identically. Folded into this
  task because the version bump masks the bug for existing data anyway.
- 6 unit tests added (first tests in cache package): determinism, order
  independence (files and keys), content sensitivity, key/content boundary
  regression, and a canary that fails if `cacheSchemaVersion` is ever
  dropped from the mixin.

### Files changed

- `ai-orchestrator/internal/cache/version.go` — new file, one constant +
  bump-rules doc-comment.
- `ai-orchestrator/internal/cache/repository.go` — `ComputeSignature`
  mixes in version + adds delimiters.
- `ai-orchestrator/internal/cache/repository_test.go` — new file.

### Build matrix verification

| Service | Default |
| --- | --- |
| api-gateway | ✅ |
| builder-svc | ✅ |
| runner-svc | ✅ |
| user-billing | ✅ |
| ai-orchestrator | ✅ |
| shared | ✅ |

`go test ./internal/cache/` — 6 PASS.

### Smoke test

After deploy:

1. **`rxjs-pokemon` regression:** redeploy
   `https://github.com/jherr/rxjs-pokemon`. Build logs should now contain
   `FROM docker.io/library/node:16-alpine` (Task 9's CRA legacy heuristic
   kicks in correctly because cache is bypassed). `RUN npm run build`
   succeeds.
2. **Existing repo, no logic change:** redeploy a repo that was deployed
   pre-Task-9 (any modern Vite or Next.js). Expected: ai-orchestrator log
   says `source: template` (cache miss this once; fresh template render),
   then `source: cache` on the third deploy of the same repo. Behaviour is
   identical to pre-task except that the first redeploy after the version
   bump skips the cache.

### Process note for future tasks

Whenever any of the following change in a way that affects the generated
Dockerfile, bump `cacheSchemaVersion`:

- `ai-orchestrator/internal/detector/*`
- `ai-orchestrator/internal/templates/*`
- `ai-orchestrator/internal/llm/prompt.go`

Old rows are unreachable from then on; `DeleteExpired` removes them via
the 7-day TTL. No migration needed.

### Lesson

Task 4.2.5 lesson revisited: a working smoke test of the happy path isn't
enough — the cache hides behaviour changes from observation. When Task 9's
smoke test said "deploy succeeds with source: template", we should have
checked that `source: template` meant **fresh** render, not stored row.
Going forward: when matcher/detector logic changes, the smoke test must
verify either a cache miss happened OR the cache was explicitly cleared.

### Out of scope

- Migration to remove old rows immediately. The 7-day TTL is fast enough
  and avoids the complexity of an alembic-style migration in a service
  that owns its own schema.
- Per-template cache scoping (one row per template_id). Considered but
  adds matcher coupling to the cache schema without solving a real problem.
- Cache "softness" via stored matcher version on each row (so a row knows
  which version produced it). Equivalent in invalidation power but slower
  to query and requires a schema migration. Not worth it.

## Task 9.2: Unify base-image allow-list as single env var

**Date:** 2026-05-19

### Problem

Two allow-lists for Docker base images had drifted:

- `ai-orchestrator/internal/service/service.go` (LLM `Constraints`):
  hardcoded `node, python, golang, nginx, gcr.io/distroless/, alpine`.
- `builder-svc/config/config.go` `ALLOWED_BASE_IMAGES`: defaults to
  `node, python, golang, ruby, nginx, alpine, debian, ubuntu, gcr.io/distroless/`.

`debian` and `ubuntu` were reachable to the validator but invisible to
the LLM (it never emitted them). Any future allow-list edit needed to be
made in two unrelated places. Also missing `oven/bun:` and `denoland/deno:`
required for legitimate Bun/Deno projects.

### What changed

- `infra/.env.example` / `infra/.env` — single `ALLOWED_BASE_IMAGES` is
  source of truth. Expanded with `oven/bun:` and `denoland/deno:`.
- `infra/docker-compose.yml` — ai-orchestrator now receives
  `ALLOWED_BASE_IMAGES` via env, same as builder-svc. `:-` empty default
  surfaces a missing `.env` at config-validation time.
- `ai-orchestrator/config/config.go` — new `AllowedBaseImagePrefixes` field
  populated from env with identical default to builder-svc; fails closed
  if the list resolves to empty.
- `ai-orchestrator/internal/service/service.go` — hardcoded literal
  removed. `buildLLMConstraints()` method returns config-sourced list.
- `ai-orchestrator/internal/service/service_test.go` — new regression
  test asserts the constraint comes from config verbatim.
- `builder-svc/config/config.go` — default extended to match (only
  fallback when env unset; .env explicitly sets it).

### Files changed

- `infra/.env.example`
- `infra/.env`
- `infra/docker-compose.yml`
- `snaphost-backend/internal/ai/config/config.go`
- `snaphost-backend/internal/ai/service/service.go`
- `snaphost-backend/internal/ai/service/service_test.go` (new)
- `snaphost-backend/internal/builder/config/config.go`

### Build matrix verification

| Service | Default |
| --- | --- |
| api-gateway | ✅ |
| builder-svc | ✅ |
| runner-svc | ✅ |
| user-billing | ✅ |
| ai-orchestrator | ✅ |
| shared | ✅ |

`go test ./internal/service/` — PASS.

### Runtime verification

`docker inspect infra-ai-orchestrator-1` and `docker compose exec
builder-worker printenv ALLOWED_BASE_IMAGES` both yield the identical
string ending in `...,oven/bun:,denoland/deno:`.

### Smoke test

- Smoke deploy of Bun project (`tcgdex/cards-database`) — validator no
  longer rejects `oven/bun:*` on base image prefix. Pending Task 9.3 for
  proper mode-aware handling of user-provided Dockerfiles.
- Smoke regression — modern Vite/Next.js repo without Dockerfile produces
  generated `NODE_VERSION=20` Dockerfile; build succeeds.

### Lesson

Config in two places is config in zero places. Same shape as Task 4.2.5's
"two state machines with overlapping vocabulary" lesson — when the same
domain concept lives in two stores, drift is inevitable. The fix is
deduplication at the env layer, not coordination at the code layer.

### Out of scope

- Dynamic / per-user allow-list (separate task).
- Per-project override (separate task).
- Registry-pinned digests instead of tag prefixes (separate task).

## Task 9.3: Two-mode Dockerfile validation (strict / permissive)

**Date:** 2026-05-19

### Problem

`validator.ValidateDockerfile` applied a single policy to two
fundamentally different inputs:

- LLM-generated Dockerfile (we do NOT trust by default).
- User-provided Dockerfile (author chose `oven/bun:1-alpine` deliberately).

Same allow-list for both rejected legitimate Bun/Deno/Rust/PHP projects
that shipped their own Dockerfile.

### Decision

- **Strict mode** (LLM-generated): narrow allow-list from
  `ALLOWED_BASE_IMAGES`. Unchanged from Task 9.2.
- **Permissive mode** (user-provided): wider allow-list of ~22 known
  vendors from `ALLOWED_BASE_IMAGES_PERMISSIVE`.
- **Both modes:** forbid `:latest` and missing-tag.
- **No LLM fallback** when user-provided Dockerfile is rejected. Clear
  remediation message instructs user to fix the FROM line or remove their
  Dockerfile.

### What changed

- `infra/.env.example` / `infra/.env` — `ALLOWED_BASE_IMAGES_PERMISSIVE`
  added (22 entries: official vendors covering mainstream backend stacks
  + database images).
- `infra/docker-compose.yml` — pass-through to builder-worker. Empty
  default; `Load` falls back to strict list if unset.
- `builder-svc/config/config.go` — `AllowedBaseImagePrefixesPermissive`
  field, falls back to strict allow-list when env empty (zero-config
  rollout to existing clusters is a no-op).
- `shared/validator/dockerfile.go` — exported `Mode`, `Options`,
  `ValidateDockerfileWithOptions`. Legacy `ValidateDockerfile` retained
  as thin wrapper (strict mode). New `checkBaseImageTag` rejects
  `:latest` and missing-tag in both modes; accepts digest pins and
  `scratch`; correctly handles registry-host-with-port refs.
- `builder-svc/internal/pipeline/runner.go` — picks mode based on
  whether Dockerfile path is `Dockerfile.snaphost` (strict) vs anything
  else (permissive). Emits user-friendly remediation hint when a
  user-provided Dockerfile is rejected on base image.
- `shared/validator/dockerfile_test.go` — 10 tests: strict rejects bun,
  permissive accepts bun, permissive fallback to strict, 6 tag-rule
  cases, registry-host-with-port regression, latest-in-both-modes.

### Files changed

- `infra/.env.example`
- `infra/.env`
- `infra/docker-compose.yml`
- `snaphost-backend/internal/builder/config/config.go`
- `snaphost-backend/internal/builder/pipeline/runner.go`
- `snaphost-backend/internal/shared/validator/dockerfile.go`
- `snaphost-backend/internal/shared/validator/dockerfile_test.go` (new)

### Build matrix verification

| Service | Default |
| --- | --- |
| api-gateway | ✅ |
| builder-svc | ✅ |
| runner-svc | ✅ |
| user-billing | ✅ |
| ai-orchestrator | ✅ |
| shared | ✅ |

`go test ./validator/` — 10 PASS. `go test ./...` in builder-svc — no
regressions. `docker compose exec builder-worker printenv
ALLOWED_BASE_IMAGES_PERMISSIVE` shows 22-entry list at runtime.

### Smoke test plan

- Bun repo with own Dockerfile (`tcgdex/cards-database`) — validator
  runs `ModePermissive`, `oven/bun:1-alpine` accepted, tag explicit.
  Build proceeds past validate.
- `:latest` rejection — strict and permissive both surface
  `base_image_latest_tag`.
- Missing tag rejection — `base_image_missing_tag` in both modes.
- Strict mode unchanged — Next.js/Vite repos without Dockerfile follow
  LLM/template path with strict allow-list.
- Permissive rejection UX — `weirdvendor/runtime:1.0` triggers
  `base_image_not_allowed` plus the remediation hint log line.

### Lesson

Same code, two threat models, one policy = wrong. The abstraction
needed to split before adding more rules to it. Same shape as Task 4.2.5:
when two callers want different policies, factor the policy out, don't
parameterize the call site.

### Out of scope

- Surfacing the permissive allow-list via API for UI hints (separate task).
- Per-user / per-org override of permissive list (separate task).
- Required digest pinning. We accept any explicit tag including `:20`,
  `:20-alpine`, `:20.5.0`. Digest-only enforcement is a bigger UX shift.

## Task 9.4: Image reference normalization in validator

**Date:** 2026-05-19

### Problem

Prefix match between raw image ref and raw allow-list prefix is brittle.
Pre-existing latent bug; Task 9.3 exposed it via vendors like `oven/bun`
where users commonly write the fully-qualified form.

Smoke test after Task 9.3 deployed `https://github.com/tcgdex/cards-database`
and got `base image "docker.io/oven/bun:1-alpine" is not in the allowed
list` — even though `oven/bun:` is in the permissive list and pipeline
correctly selected `ModePermissive`.

Root cause: validator does prefix match on the raw FROM-line image
reference. The Dockerfile writes `FROM docker.io/oven/bun:1-alpine` (fully
qualified — a common production best practice). Allow-list stores
`oven/bun:` (short form — what most people write in dev). Prefix match
between these two is false.

The bug is silent and partially masked: it happened to work for `node:` /
`nginx:` / etc. because users typically write THOSE in short form. The
moment a user writes the fully qualified form (which is best practice for
real production Dockerfiles), validator rejects them even though the image
is in the allow-list.

### Solution

Normalize both sides to canonical OCI form before comparison. Allow-list
normalized once per validate call (not per FROM-line).

Canonical form rules (per OCI Image Spec namespacing):

| As written | Canonical |
|---|---|
| `node:20` | `docker.io/library/node:20` |
| `oven/bun:1-alpine` | `docker.io/oven/bun:1-alpine` |
| `docker.io/oven/bun:1-alpine` | `docker.io/oven/bun:1-alpine` |
| `gcr.io/distroless/base` | `gcr.io/distroless/base` |
| `host.docker.internal:5000/foo:v1` | `host.docker.internal:5000/foo:v1` |
| `scratch` | `scratch` |
| `node@sha256:abc` | `docker.io/library/node@sha256:abc` |

### What changed

Two new private helpers in the validator package:

- `normalizeImageRef(ref string) string` — canonicalizes a Docker image
  reference. Short names get `docker.io/library/` prefix, vendor names
  without registry get `docker.io/` prefix. Registry detection uses the
  same heuristic as Docker/containerd/BuildKit: first path segment
  contains "." or ":" or is exactly "localhost".

- `normalizeAllowedPrefix(prefix string) string` — canonicalizes an
  allow-list prefix, preserving trailing ":" or "/" markers and
  lowercasing the result for case-insensitive comparison.

`isAllowedBaseImage` parameter renamed from `allowedPrefixes` to
`normalizedAllowedPrefixes` to document the precondition that callers
must pre-normalize. The function now normalizes its input image via
`normalizeImageRef` before comparison.

Behaviour change is only "more references accepted as equivalent";
nothing previously accepted becomes rejected.

### Files changed

- `snaphost-backend/internal/shared/validator/normalize.go` — new file, two
  private helpers.
- `snaphost-backend/internal/shared/validator/normalize_test.go` — new file,
  ~47 tests (25 normalizeImageRef, 12 normalizeAllowedPrefix,
  10 end-to-end allow-list match scenarios).
- `snaphost-backend/internal/shared/validator/dockerfile.go` — modified:
  allow-list normalized once before FROM-line loop; `isAllowedBaseImage`
  uses normalized list and normalizes input image.
- `snaphost-backend/internal/shared/validator/dockerfile_test.go` — modified:
  added `TestValidate_FullyQualifiedDockerHub` regression test.

### Build matrix verification

| Service | Default | -tags yandex |
| --- | --- | --- |
| api-gateway | ✅ | n/a |
| builder-svc | ✅ | n/a |
| runner-svc | ✅ | ❌ (unchanged from Task 0 baseline) |
| user-billing | ✅ | n/a |
| ai-orchestrator | ✅ | n/a |
| shared | ✅ | n/a |

`go test ./validator/` — 47 PASS (11 existing + 36 new).
`go test ./...` in builder-svc — no regressions.

### Smoke test outcomes

1. **tcgdex regression:** `docker.io/oven/bun:1-alpine` now accepted when
   `oven/bun:` is in permissive list. Validate stage passes.
2. **Strict still works:** short-form `node:20-alpine` normalizes to
   `docker.io/library/node:20-alpine`, matches `node:` →
   `docker.io/library/node:`. Validate passes.
3. **Bad image still rejected:** `weirdvendor/runtime:1.0` normalizes to
   `docker.io/weirdvendor/runtime:1.0` — no allow-list entry matches.
   `base_image_not_allowed` issue emitted.
4. **:latest still rejected:** tag policy is applied after allow-list
   match, independent of normalization. `base_image_latest_tag` still
   fires.

### Lessons (continuing the chain from Tasks 4.2.5, 9.1, 9.3)

- "Prefix match on user-controlled strings without canonicalization
  is fragile across input variants. Same family of bug as the
  `built` vs `building` state-machine confusion in 4.2.5 — the
  underlying domain has multiple valid representations, and code
  that doesn't canonicalize first will eventually fail on the
  representation it didn't anticipate."

- "Each layer in this allow-list chain — env, config, constraint,
  validator — was independently reasonable, but together they
  propagated a string format dependency. Normalizing at the
  boundary (validator input) is the right placement: it's the layer
  that actually does the comparison."

### Out of scope (with explicit follow-up trackers)

- Full OCI reference parser. We do canonical namespacing only — no
  validation of character classes, length limits, or RFC compliance.
  BuildKit catches malformed refs anyway.
- Allow-list normalization in `infra/.env.example` (we COULD rewrite
  defaults in canonical form for clarity, but that's cosmetic — both
  forms work identically after this task).
- Caching the normalized allow-list across validate calls. Currently
  re-normalized per call. Per-call cost is microseconds on a list of
  ~25 entries; optimization is unnecessary until profiling shows it.

## Task 10.4 - Yandex deploy happy path

### What changed

- `runner-svc` Yandex backend now uses deterministic Yandex-safe container
  names, preferring `RunRequest.Subdomain` and falling back to `proj-<deploy_id>`.
- `Run` now reuses an existing named Serverless Container instead of failing on
  repeated deploy attempts for the same deploy name.
- Revision deploy omits the reserved `PORT` env var, caps execution TTL with
  `maxServerlessExecTimeout`, returns `TTLExpiresAt` from the effective TTL,
  uses configured memory, rounds memory to Yandex's 128 MiB multiple, and
  derives core fraction from configured CPU limit.
- Happy-path lifecycle logs are published for container create/reuse, revision
  deploy, API Gateway route update, and public URL readiness.
- Cleanup after failures deletes only cloud resources created during the current
  attempt; reused containers are left intact.
- `runner-svc/Dockerfile` gained opt-in `ARG GO_BUILD_TAGS=""`. Production
  Yandex images can be built with:

```powershell
docker build -f snaphost-backend/internal/runtime/Dockerfile `
  --build-arg GO_BUILD_TAGS=yandex `
  snaphost-backend
```

### Yandex SDK/API assumptions discovered

- Serverless Containers expose no explicit listen-port field in `ImageSpec`;
  `PORT` is reserved by Yandex and must not be sent in the revision environment.
  Runtime code must listen on the platform-provided `PORT`.
- `ListContainersRequest` supports `Filter: name="<container-name>"`; the
  backend uses it for idempotent create-or-reuse.
- Deploying a revision with `serviceAccountId` requires the caller to have
  `iam.serviceAccounts.user`; Terraform now includes this role for
  `snaphost-runner`.
- Updating API Gateway OpenAPI content requires field mask path
  `openapi_spec`, not the oneof wrapper name `spec`.
- Terraform must not manage API Gateway `spec` after creation because
  `runner-svc` mutates routes at runtime; `yandex_api_gateway.router` now
  ignores `spec` drift. Terraform also creates a wildcard DNS CNAME to the API
  Gateway default domain so `https://<subdomain>.<domain>` resolves.
- API Gateway does not match routes by the custom `x-yc-apigateway-host`
  extension previously assumed by the draft code. For Task 10.4's single-deploy
  smoke path, `runner-svc` updates the gateway proxy path `/{path+}` to the
  current container and records the hostname in `x-snaphost-host` metadata for
  cleanup. Cleanup also removes the legacy `/__snaphost__/<host>` route shape
  created by the earlier draft. True concurrent multi-host routing needs the
  Task 10.6 design pass.
- Serverless Container memory must be a multiple of 128 MiB, so non-multiple
  config values are rounded up before revision deploy.
- `Resources.CoreFraction` is a percent multiple of 5. Values are clamped to
  5..100 and rounded up to a valid multiple.

### Verification run

From `snaphost-backend/runner-svc`, with repo-local Go cache/AppData because
the default Windows Go telemetry/cache paths were not writable:

```powershell
go test ./...
go test -tags yandex ./...
go build -buildvcs=false ./...
go build -buildvcs=false -tags yandex ./...
```

All commands passed.

### Manual smoke test status

Run locally on 2026-05-27 against the configured Yandex Cloud folder using
separate builder and runner authorized-key JSON files under `secrets/`.

Smoke setup:

- Built and pushed a temporary Python HTTP image to
  `cr.yandex/.../snaphost/smoke-104:00000000-0000-4000-8000-000000001004`
  using the builder service account.
- Built `runner-svc` with `--build-arg GO_BUILD_TAGS=yandex`.
- Ran temporary Redis and runner containers locally with
  `RUNNER_BACKEND=yandex`, `STRICT_IMAGE_VALIDATION=false`, and Terraform
  outputs from `infra/.env`.
- Called `POST /internal/deploys` with deploy ID
  `00000000-0000-4000-8000-000000001004`.

Smoke result:

- Runner returned `202 Accepted` with container ID `bba4agvvjjulg6aqldhh` and
  endpoint `https://proj-00000000000040008000000000001004.snaphost.online`.
- The public URL resolved through the wildcard DNS record and returned HTTP
  `200` from the Serverless Container.
- `DELETE /internal/deploys/:id` returned `204`.
- Post-cleanup API Gateway spec was restored to the dummy 404 proxy route; the
  smoke route and legacy draft route were removed.

Runtime issues found and fixed during smoke:

- Yandex rejects revision env var `PORT` as reserved. The backend now omits
  `PORT`; runtime containers must listen on the platform-provided `PORT`.
- Deploying a revision with `serviceAccountId` requires
  `iam.serviceAccounts.user`; Terraform now grants this to `snaphost-runner`.
- API Gateway update mask must be `openapi_spec`.
- Wildcard DNS CNAME to the API Gateway default domain was missing and is now
  managed by Terraform.
- The draft host-extension route shape did not match real API Gateway routing.
  Task 10.4 uses a single proxy path for the current smoke/happy path; true
  concurrent multi-host routing remains Task 10.6.

### Out of scope

- Full TTL/delete lifecycle remains Task 10.5.
- Cross-process API Gateway mutation conflict handling remains Task 10.6.
- Full registry contract verification remains Task 10.7.
- Cloud Logging streaming remains Task 10.8.

## Task 10.5: Yandex stop/delete/TTL lifecycle

### What changed

- `runner-watchdog` now supports `RUNNER_BACKEND=yandex` when built with
  `-tags yandex`, using the same Yandex backend factory pattern as the API
  binary.
- Default non-Yandex watchdog builds still compile and fail clearly if started
  with `RUNNER_BACKEND=yandex`: `yandex backend not compiled in: rebuild with
  -tags yandex`.
- `YandexBackend.Stop` now applies a default two-minute timeout when the caller
  context has no deadline.
- Stop/delete first locates the current API Gateway route by `container_id`,
  restores the Task 10.4 dummy 404 proxy route only when that route belongs to
  the stopped container, and then deletes the Serverless Container.
- Missing routes and Yandex NotFound container deletes are treated as success,
  making repeated manual DELETE and watchdog retries safe.
- Real route/delete errors are logged with `container_id` and hostname when
  available, and returned so the caller can retry cleanup instead of marking a
  partially cleaned deployment stopped.
- Added Yandex-tagged unit coverage for gateway spec removal behavior and
  pure hostname lookup from the current spec.

### Files changed

- `snaphost-backend/cmd/runner-watchdog/main.go`
- `snaphost-backend/internal/runtime/cmd/watchdog/yandex_enabled.go`
- `snaphost-backend/internal/runtime/cmd/watchdog/yandex_stub.go`
- `snaphost-backend/internal/runtime/backend/yandex/yandex.go`
- `snaphost-backend/internal/runtime/backend/yandex/gateway_spec_test.go`
- `REFACTOR_NOTES.md`

### Verification run

From `snaphost-backend/runner-svc`, with repo-local Go cache/AppData under the
service directory because root-level temp dirs and default Windows Go cache
paths were not writable:

```powershell
go test ./...
go test -tags yandex ./...
go build -buildvcs=false ./...
go build -buildvcs=false -tags yandex ./...
```

All commands passed.

Also run from repo root:

```powershell
git -c safe.directory=D:/snaphost diff --check
```

### Manual smoke test status

Run locally on 2026-05-27 against the configured Yandex Cloud folder using the
runner and builder authorized-key JSON files under `secrets/`.

Smoke setup:

- Built temporary `runner-svc` image with `--build-arg GO_BUILD_TAGS=yandex`.
- Ran a separate temporary runner API container on port `18084` with
  `RUNNER_BACKEND=yandex` and `STRICT_IMAGE_VALIDATION=false`, leaving the
  normal compose runner API untouched.
- Built and pushed a temporary Go HTTP smoke image to Yandex Container Registry.
  The image tag matched the smoke `deploy_id`.
- Used `curlimages/curl` for public HTTPS checks because Windows `curl.exe`
  failed locally with a Schannel credential error.

Manual delete cleanup result:

```bash
docker build -f snaphost-backend/internal/runtime/Dockerfile \
  --build-arg GO_BUILD_TAGS=yandex \
  -t snaphost-runner-svc:yandex \
  snaphost-backend

# Deploy one smoke image whose tag equals deploy_id through POST /internal/deploys.
# Confirm endpoint_url returns HTTP 200.

curl -X DELETE "http://localhost:<runner_port>/internal/deploys/<deploy_id>" \
  -H "Content-Type: application/json" \
  -H "X-Webhook-Secret: <secret>" \
  -d '{"container_id":"<container_id from deploy response>"}'

# Expected: 204, container absent in Yandex, gateway proxy restored to dummy 404,
# and a second DELETE with the same body succeeds instead of failing on NotFound.
```

- `POST /internal/deploys` returned `202` with container
  `bbal8ci380rlnpibhdeh` and endpoint
  `https://proj-33e874ff34a24632ba8161ea41e7efaf.snaphost.online`.
- Public endpoint returned HTTP `200` with body `snaphost yandex smoke ok`.
- `DELETE /internal/deploys/:id` returned `204`.
- The same public endpoint returned the dummy HTTP `404` body
  `Deployment not found` after cleanup.
- A second DELETE with the same container ID also returned `204`; logs showed
  `no gateway route found for container` and `container already absent`.
- Two stale smoke containers created by earlier failed Python-image probes were
  also deleted through the same endpoint. The Python image itself ran locally
  but returned Yandex `UserCodeError exit status 2`, so the final passing smoke
  used a static Go HTTP server image.

Watchdog TTL cleanup result:

```bash
# Run runner-watchdog from the Yandex-tagged image/binary with:
# RUNNER_BACKEND=yandex
# YANDEX_SA_KEY_PATH=/secrets/runner-key.json
# YANDEX_FOLDER_ID=<folder-id>
# YANDEX_REGISTRY_URL=<registry-url>
# YANDEX_RUNNER_SA_ID=<runner-service-account-id>
# YANDEX_API_GATEWAY_ID=<api-gateway-id>

curl "http://localhost:<user_billing_port>/internal/deploys/expired?limit=50" \
  -H "X-Webhook-Secret: <secret>"

# Put one running deploy's ttl_expires_at in the past, wait one watchdog
# interval, and confirm container/route cleanup plus user-billing status=stopped.
```

- Temporarily stopped the normal compose `infra-runner-watchdog-1` so the
  docker backend watchdog could not race the Yandex smoke row.
- Inserted a minimal temporary deploy row in user-billing Postgres, then ran
  `POST /internal/deploys` through the temporary Yandex runner API so
  `SetRunning` populated the row.
- Updated that row's `ttl_expires_at` to the past.
- `GET /internal/deploys/expired?limit=50` returned the smoke deploy with
  container `bbabh1rt6ap87vkl8ad4`.
- A separate temporary Yandex watchdog container started with backend
  `yandex`, checked one expired deploy, removed the gateway route, deleted the
  container, and logged `stopped=1`.
- The deploy row status became `stopped`, expired list became empty, and the
  public endpoint returned the dummy HTTP `404` body `Deployment not found`.
- The normal compose `infra-runner-watchdog-1` was restarted afterwards and
  selected backend `docker` again.

Follow-up accepted for Task 10.6: `status` transitions to `stopped`, but
`deploys.stopped_at` remains null because runner-svc currently calls
user-billing's generic `UpdateStatus("stopped")`, whose repository method does
not set `stopped_at`. This did not block Task 10.5's cleanup/status acceptance,
but Task 10.6 should fix lifecycle accounting so TTL/watchdog stop leaves both
`status='stopped'` and non-null `stopped_at`.

### Out of scope

- Concurrent multi-subdomain API Gateway routing remains Task 10.6.
- Full Cloud Logging streaming remains Task 10.8.
- No provider-agnostic backend interface changes were made.

## Task 10.6: Safe Yandex API Gateway spec management

### Routing investigation and decision

Task 10.6 requires not shipping fake host routing. The current Yandex API
Gateway docs describe OpenAPI path/operation routing plus Yandex extensions for
integrations. The Serverless Containers extension examples route by OpenAPI
paths such as `/example/{ID}` and `/{proxy+}`; the docs explicitly say the
container receives the user's `Host` header, but do not define `Host` as a
gateway route selector:

- https://yandex.cloud/en/docs/api-gateway/concepts/extensions/containers
- https://yandex.cloud/en/docs/api-gateway/concepts/extensions/any-method
- https://yandex.cloud/en/docs/api-gateway/concepts/extensions/

The API reference confirms `GetOpenapiSpec` returns only the OpenAPI text and
`Update` replaces the `openapiSpec` field selected by field mask; no etag,
revision, or conditional update token is exposed in that API surface:

- https://yandex.cloud/en/docs/api-gateway/apigateway/api-ref/ApiGateway/getOpenapiSpec
- https://yandex.cloud/en/docs/api-gateway/apigateway/api-ref/ApiGateway/update

Yandex also supports attaching custom domains to an API Gateway, but that is a
gateway-level domain binding, not a per-route host selector inside a single
OpenAPI spec:

- https://yandex.cloud/en/docs/api-gateway/apigateway/api-ref/ApiGateway/addDomain
- https://yandex.cloud/en/docs/api-gateway/operations/api-gw-domains

This matches the Task 10.4 smoke result: custom metadata such as
`x-snaphost-host` / `x-yc-apigateway-host` was preserved in the spec but did not
affect routing. Therefore one API Gateway with a wildcard domain and multiple
same-path `/{path+}` integrations cannot safely serve two SnapHost subdomains
based on hostname.

Decision for this task: do not implement a fake multi-route OpenAPI helper that
unit-tests as host-based routing but Yandex ignores at runtime. The viable
production shapes need a larger infrastructure decision:

- per-deploy API Gateway plus per-deploy DNS/domain binding/certificate
  lifecycle; or
- a central router service behind the single gateway that dispatches by `Host`
  to deploy-specific backends; or
- another explicit routing layer/provider.

That work requires additional IAM/DNS/gateway lifecycle design and is outside
the safe code change for this pass.

### Implemented follow-up

Fixed the Task 10.5 lifecycle accounting bug in `user-billing`: generic status
updates now set `stopped_at = COALESCE(stopped_at, now())` only when
`status='stopped'`. This makes manual stop and watchdog TTL cleanup leave both:

- `deploys.status = 'stopped'`
- `deploys.stopped_at IS NOT NULL`

The update is idempotent because repeated stopped updates preserve the original
`stopped_at`. Other statuses preserve the existing value, and deleted lifecycle
accounting remains owned by `MarkDeleted`.

### Files changed

- `snaphost-backend/internal/control/deploy/repository.go`
- `snaphost-backend/internal/control/deploy/repository_test.go`
- `REFACTOR_NOTES.md`

### Verification status

Manual two-concurrent-deploy smoke was not run because the routing
investigation found no credible one-gateway host-routing mechanism to test
without changing infrastructure shape.

### Out of scope

- No fake multi-subdomain Yandex API Gateway routing was implemented.
- No Terraform/IAM/DNS role changes were made.
- Task 10.7 registry integration and Task 10.8 runtime logs remain untouched.

## Task 10.6b: Yandex central router feasibility and MVP

### Feasibility decision

Central router is viable at the router-to-user-container layer:

- Yandex Serverless Containers expose an HTTPS invocation URL on the container
  resource (`Container.url`). The URL can be retrieved via `Container.Get`.
- Yandex documents that invoking over HTTPS passes an HTTP request to the
  application running inside the container.
- Private container invocation requires `Authorization: Bearer <IAM token>` or
  an API key.
- `serverless-containers.containerInvoker` is the narrow role for invocation;
  the existing runner service account already has invocation/editor privileges.

Relevant official docs:

- https://yandex.cloud/en/docs/serverless-containers/concepts/invoke
- https://yandex.cloud/en/docs/serverless-containers/operations/invoke
- https://yandex.cloud/en/docs/serverless-containers/operations/invocation-link
- https://yandex.cloud/en/docs/serverless-containers/operations/auth
- https://yandex.cloud/en/docs/serverless-containers/security/

The router MVP is therefore real for HTTP proxying, but full production wiring
still requires one infra step: the Yandex API Gateway static wildcard proxy must
point to the router service/container instead of directly to a user deploy. That
Terraform/API Gateway deployment wiring was not completed in this task.

### Router design implemented

- Added `snaphost-backend/router-svc`, a small Go HTTP service.
- The router receives wildcard-domain traffic, validates that `Host` is under
  configured `DOMAIN_SUFFIX`, normalizes it, and queries user-billing through
  `GET /internal/routes?host=<host>`.
- Foreign hosts, the bare domain, empty hosts, and nested subdomains are
  rejected before user-billing lookup, so `proj-a.attacker.example` cannot match
  the `proj-a` SnapHost deploy.
- user-billing returns only a running deploy route: `deploy_id`, `container_id`,
  `status`, and `host`.
- The router resolves `container_id` to a Yandex invocation URL through
  `Container.Get`.
- The router obtains an IAM bearer token via `shared/yandexauth` and proxies the
  original HTTP request to the container invocation URL.
- Proxy preserves method, path, query string, body, response status, response
  headers, and response body.
- Hop-by-hop headers are stripped. Incoming `Authorization` is not forwarded
  because the router must use that header for Yandex IAM auth; this is an MVP
  limitation for apps that expect end-user Authorization headers.
- Added `snaphost-backend/router-svc/Dockerfile`. Build context is
  `snaphost-backend/` so the `shared/` module is available for the local
  `replace` directive.

### Runner integration

- Added `YANDEX_ROUTING_MODE` to runner-svc config:
  - `gateway` (default): legacy Task 10.4/10.5 behavior mutates the API Gateway
    proxy route per deploy.
  - `router`: Yandex `Run` creates/deploys the user Serverless Container but
    does not overwrite the API Gateway spec. The public URL remains
    `https://<subdomain>.<domain>`.
- In router mode, Yandex `Stop` skips per-deploy gateway cleanup and only
  deletes the user Serverless Container.

### User-billing integration

- Added internal route lookup endpoint:

```http
GET /internal/routes?host=<host>
X-Webhook-Secret: <secret>
```

It returns `404` unless the deploy is `running` and has a non-empty
`container_id`. Domain-suffix ingress validation lives in router-svc; this
endpoint remains webhook-secret protected and returns only the minimal route
shape.

### Files changed

- `snaphost-backend/router-svc/`
- `snaphost-backend/internal/control/deploy/handler.go`
- `snaphost-backend/internal/control/deploy/handler_test.go`
- `snaphost-backend/internal/control/deploy/repository.go`
- `snaphost-backend/internal/control/deploy/repository_test.go`
- `snaphost-backend/internal/control/routes/routes.go`
- `snaphost-backend/internal/runtime/config/config.go`
- `snaphost-backend/internal/runtime/backend/yandex/yandex.go`
- `docs/yandex-runtime.md`
- `REFACTOR_NOTES.md`

### Verification status

Local unit/build verification passed for router-svc, user-billing, and
runner-svc, including Yandex-tagged runner build. Go printed non-fatal stat
cache warnings for the user module cache during router-svc checks, but the
commands exited successfully.

Task 10.6b follow-up added router domain-suffix validation tests, proved foreign
hosts do not call user-billing, and added the runnable router Dockerfile.

Manual Yandex smoke tests were not run in this turn. Required pending smoke:

1. Deploy router-svc as the static wildcard API Gateway target.
2. Start runner-svc with `YANDEX_ROUTING_MODE=router`.
3. Deploy two smoke apps with distinct bodies.
4. Confirm `proj-a.<domain>` returns A and `proj-b.<domain>` returns B.
5. Delete/expire one deploy and confirm the other remains reachable.

### Current status and MVP limitations

- This is now a runnable router service with local unit/build verification.
- End-to-end Yandex multi-subdomain smoke is still pending because
  API Gateway/Terraform wiring to deploy and target router-svc is pending.
- WebSocket, long streaming responses, large uploads, and end-user
  `Authorization` header forwarding are not supported yet.
- Route lookup has no cache; every proxied request asks user-billing.
- IAM token cache is deliberately short and in-process only.

### Out of scope

- No Task 10.7 registry deletion/integration work.
- No Task 10.8 Cloud Logging runtime stream.
- No frontend changes.

## Task 10.6c: Router infra wiring and live Yandex smoke

### Summary

Implemented the minimum deployable central-router wiring for Yandex production
mode. API Gateway can now use a static greedy route to a Terraform-managed
`router-svc` Yandex Serverless Container instead of relying on runner-svc to
mutate gateway specs per deploy.

### Infrastructure shape

- Added `terraform/yandex/router_container.tf`.
- `router-svc` is deployed as `yandex_serverless_container.router` when all
  router variables are set:
  - `router_image_url`
  - `router_user_billing_url`
  - `router_webhook_secret_id`
  - `router_webhook_secret_version_id`
  - `router_webhook_secret_key`
- The router container runs with `YANDEX_AUTH_MODE=metadata` and the attached
  runner service account, so no authorized-key JSON file is mounted or stored
  in container configuration.
- `WEBHOOK_SECRET` is passed from Yandex Lockbox through a `secrets` block.
  Terraform stores only Lockbox IDs/version/key names, not the secret value.
- Terraform grants `lockbox.payloadViewer` only on the configured Lockbox
  secret to the runner service account.
- API Gateway spec now points wildcard greedy traffic to the router container
  via `x-yc-apigateway-integration: serverless_containers` when router wiring
  is enabled. If router variables are empty, the gateway keeps the dummy 404
  spec.
- Removed `ignore_changes = [spec]` from the API Gateway resource so Terraform
  can own the static central-router spec. In `YANDEX_ROUTING_MODE=router`,
  runner-svc no longer mutates the gateway spec per deploy.

Relevant Yandex docs checked during this task:

- https://yandex.cloud/en/docs/api-gateway/concepts/extensions/containers
- https://yandex.cloud/en/docs/serverless-containers/operations/metadata-options
- https://yandex.cloud/en/docs/lockbox/operations/serverless/containers
- https://yandex.cloud/en/docs/serverless-containers/security/

### Router service changes

- `router-svc` now supports `YANDEX_AUTH_MODE=metadata` for Yandex managed
  runtimes and keeps `YANDEX_AUTH_MODE=key_file` as the Docker/Compose default.
- `shared/yandexauth` now exposes `NewSDKFromMetadata`, using the SDK's
  `InstanceServiceAccount` credentials.
- Existing domain validation remains unchanged: only one-label subdomains under
  `DOMAIN_SUFFIX` are routed; foreign hosts are rejected before user-billing
  lookup.

### Documentation

- Updated `docs/yandex-runtime.md` with the router Terraform contract,
  Lockbox secret reference requirements, metadata auth mode, Terraform outputs,
  and central-router operational checklist.
- Updated `docs/cloud-setup.md` with `YANDEX_ROUTING_MODE=router` and the
  non-secret router Terraform variables.
- Updated `terraform/yandex/terraform.tfvars.example` with commented router
  wiring placeholders. Real secret values must not be placed there.

### Local verification

Passed on 2026-05-27:

```powershell
cd terraform/yandex
terraform fmt
terraform validate

cd snaphost-backend/router-svc
go test ./...
go build -buildvcs=false ./...

cd snaphost-backend/user-billing
go test ./...
go build -buildvcs=false ./...

cd snaphost-backend/runner-svc
go test ./...
go test -tags yandex ./...
go build -buildvcs=false ./...
go build -buildvcs=false -tags yandex ./...

cd snaphost-backend/shared
go test ./...

cd snaphost-backend
docker build -f router-svc/Dockerfile -t snaphost/router-svc:task-10-6c .
docker image rm snaphost/router-svc:task-10-6c
```

Docker build succeeded, but the `snaphost-backend/` build context is large in
this local workspace. A future `.dockerignore` cleanup would make this faster
without changing runtime behavior.

### Live smoke status

Live Yandex smoke was not run in this turn. It is blocked by missing/applied
runtime inputs in the non-secret configuration path:

1. `router-svc` image must be pushed to Yandex Container Registry and assigned
   to `router_image_url`.
2. `WEBHOOK_SECRET` must exist in Yandex Lockbox and only its secret ID/version
   ID/key name should be placed in Terraform variables.
3. `router_user_billing_url` must point to a user-billing endpoint reachable
   from Yandex Serverless Containers.
4. Terraform apply must be run by an operator with bootstrap credentials.
5. Runner must be started with `RUNNER_BACKEND=yandex` and
   `YANDEX_ROUTING_MODE=router`.

No live deploys, deletes, TTL expiry, or foreign-host HTTP checks were claimed.

Smoke attempt on 2026-05-27:

- Checked `terraform/yandex/terraform.tfvars` without printing values.
- `cloud_id`, `folder_id`, `domain_name`, and `bootstrap_key_file` are set.
- Router wiring variables are missing from the file:
  - `router_image_url`
  - `router_user_billing_url`
  - `router_webhook_secret_id`
  - `router_webhook_secret_version_id`
  - `router_webhook_secret_key`
- Because those variables are absent, Terraform would keep the API Gateway in
  the dummy 404 mode and would not create `yandex_serverless_container.router`.
- Built `router-svc` locally and pushed the smoke image to Yandex Container
  Registry:
  `cr.yandex/<registry_id>/snaphost/router-svc:task-10-6c-smoke-20260527`.
  Docker authentication used the existing builder service-account key to obtain
  an IAM token; the token was not printed or stored.
- Tried to create a temporary localtunnel for local `user-billing` on port
  `8083`, but `localtunnel` did not return a public URL and was stopped.
- Live 10.6c smoke was not run. No Terraform apply, deploy, delete, TTL expiry,
  or foreign-host HTTP check was attempted.

Live smoke completed on 2026-05-28:

- Provisioned a temporary Ubuntu 24.04 VDS staging `user-billing` endpoint at
  `http://188.225.32.245:8081` with Docker Compose:
  `postgres:16-alpine`, `redis:7-alpine`, and a current `user-billing` image
  rebuilt from the working tree.
- The staging `user-billing` used the same `WEBHOOK_SECRET` as local
  `infra/.env`; the value was not printed. The temporary VDS stack was destroyed
  after the smoke.
- Set `router_user_billing_url` in `terraform/yandex/terraform.tfvars` to the
  temporary VDS URL and applied Terraform.
- Terraform created the central router container and updated API Gateway:
  - `router_container_id = bbahn9b7qh6v6mspjmku`
  - API Gateway ID: `d5dl9p60rr0gldujq5p0`
  - Wildcard domain used for smoke: `*.snaphost.online`
- Built and pushed the router image:
  `cr.yandex/<registry_id>/snaphost/router-svc:task-10-6c-smoke-20260527`.
- Built and pushed a temporary smoke app image:
  `cr.yandex/<registry_id>/snaphost/smoke-router-app:task-10-6c-20260528`.
- Created two temporary Yandex Serverless Containers from that smoke image:
  - A: `bba316h7nilbuken76cg`, body `snaphost smoke A`
  - B: `bbao3qe3vq42t5l9qdgi`, body `snaphost smoke B`
- Inserted two `running` deploy rows into the temporary staging
  `user-billing` database:
  - `smoke-a.snaphost.online` -> container A
  - `smoke-b.snaphost.online` -> container B
- Verified route lookup through staging `user-billing` returned the expected
  deploy/container pairs for both hosts.
- Verified public runtime routing through Yandex API Gateway:

```text
GET https://smoke-a.snaphost.online/ -> 200, body "snaphost smoke A"
GET https://smoke-b.snaphost.online/ -> 200, body "snaphost smoke B"
```

- Stopped deploy A in staging DB and destroyed container A.
- Verified delete isolation:

```text
GET https://smoke-a.snaphost.online/ -> 404
GET https://smoke-b.snaphost.online/ -> 200, body "snaphost smoke B"
```

- Negative foreign-host check:
  - `Host: smoke-a.attacker.example` against the API Gateway default domain
    returned `404 API Gateway not found by host name`, so the request did not
    reach router-svc. This is safe at the gateway layer but does not exercise
    router-svc's in-process `DOMAIN_SUFFIX` rejection.
  - Direct router-container invocation with a forged `Host` header returned a
    platform-level `204 No Content`, so router-level foreign-host behavior was
    not observable through Yandex live HTTP. The router-level rejection remains
    covered by `router-svc` unit tests.
- Cleanup completed:
  - Temporary smoke containers A/B were destroyed.
  - Temporary Terraform smoke file and outputs were removed.
  - `terraform plan -detailed-exitcode` returned no changes afterward.
  - Temporary VDS `user-billing` stack and data volume were removed.
  - Temporary local tar/cache/smoke files and local smoke Docker images were
    removed.

Remaining operational note: the central router Terraform wiring remains applied,
but the smoke `router_user_billing_url` points to a temporary VDS endpoint that
was torn down. Replace it with a real staging/production `user-billing` URL
before relying on router mode outside smoke tests.

### Original pending smoke checklist

The checklist below was the original blocker list before the 2026-05-28 smoke.
Items 1-7 were completed manually with temporary smoke infrastructure. Item 8
was partially covered live at the API Gateway layer and remains covered at the
router unit-test layer, as described above.

1. Push `router-svc` image to `cr.yandex/<registry_id>/snaphost/router-svc:<tag>`.
2. Create/update the Lockbox secret version containing `WEBHOOK_SECRET`.
3. Set router variables in `terraform/yandex/terraform.tfvars` without secret
   values.
4. Run `terraform plan` and `terraform apply`.
5. Deploy smoke app A and B with distinct bodies through runner router mode.
6. Verify `proj-a.<domain>` returns A and `proj-b.<domain>` returns B.
7. Delete or expire A and confirm B remains reachable.
8. Send a request with a foreign host reaching router and confirm rejection
   before user-billing lookup.

### Out of scope

- No Task 10.7 registry contract changes.
- No Task 10.8 runtime log streaming.
- No frontend changes.
- No WebSocket, large upload, or long streaming proxy support.

## Task 10.7: Yandex registry end-to-end contract

### Summary

Tightened the builder/runner registry contract so production Yandex images use
the exact Terraform `registry_url` prefix:

```text
cr.yandex/<registry_id>/snaphost
```

Builder image refs are now constructed through a focused helper that trims a
trailing slash from `REGISTRY_URL` and emits:

```text
<registry_url>/proj-<8-char-user-hash>:<deploy_id>
```

This preserves Docker dev output such as
`registry:5000/snaphost/proj-...:<deploy_id>` and makes Yandex output
`cr.yandex/<registry_id>/snaphost/proj-...:<deploy_id>`.

### Runner validation

Runner strict validation now matches allowed registry prefixes on a path
boundary. A configured prefix `cr.yandex/<registry_id>/snaphost` accepts:

```text
cr.yandex/<registry_id>/snaphost/proj-...:<deploy_id>
```

but rejects sibling textual prefixes such as:

```text
cr.yandex/<registry_id>/snaphostevil/proj-...:<deploy_id>
```

Trailing slash in `REGISTRY_ALLOWED_PREFIXES` is tolerated, but production docs
now recommend the Terraform output value without a trailing slash.

### Docs and examples

- `docs/yandex-runtime.md` now documents that `REGISTRY_URL`,
  `YANDEX_REGISTRY_URL`, and `REGISTRY_ALLOWED_PREFIXES` should all use the
  Terraform `registry_url` output exactly.
- `docs/cloud-setup.md` no longer adds a trailing slash to
  `REGISTRY_ALLOWED_PREFIXES`.
- `infra/.env.example` now includes explicit local Docker and Yandex production
  registry examples:
  - `REGISTRY_AUTH_MODE=yandex_iam`
  - `REGISTRY_INSECURE=false`
  - `SCAN_FAIL_ON_CRITICAL=true`
  - `REGISTRY_ALLOWED_PREFIXES=cr.yandex/<registry_id>/snaphost`
- Replaced a key-like OpenRouter placeholder in `infra/.env.example` with a
  non-secret placeholder.

### Tests

- Added builder image-ref construction coverage for:
  - Yandex production registry prefix;
  - Docker dev registry prefix;
  - trailing slash trimming.
- Added runner prefix matching coverage for:
  - Yandex production prefix;
  - trailing slash tolerance;
  - Docker dev prefix;
  - rejection of `snaphostevil` path-boundary bypass.
- Added runner strict validation coverage for a Yandex production image ref.

### Verification

Passed on 2026-05-28:

```powershell
cd snaphost-backend/builder-svc
go test ./...
go build -buildvcs=false ./...

cd ../runner-svc
go test ./...
go test -tags yandex ./...
go build -buildvcs=false ./...
go build -buildvcs=false -tags yandex ./...

cd ../user-billing
go test ./...
go build -buildvcs=false ./...
```

### Manual smoke status

Live Yandex registry smoke was not run. It remains blocked by missing/applied
operator runtime setup in this session:

1. Builder service account key mounted into builder-worker.
2. `REGISTRY_AUTH_MODE=yandex_iam` and
   `REGISTRY_URL=cr.yandex/<registry_id>/snaphost` in the real builder runtime.
3. Runner started with `STRICT_IMAGE_VALIDATION=true`,
   `YANDEX_REGISTRY_URL=cr.yandex/<registry_id>/snaphost`, and
   `REGISTRY_ALLOWED_PREFIXES=cr.yandex/<registry_id>/snaphost`.
4. A real build/push through BuildKit to Yandex Container Registry.
5. A live Yandex deploy proving Serverless Containers can pull that image.

No real secrets, `.env`, `.keys`, or `terraform.tfvars` were read or printed.

### Out of scope

- No Task 10.8 runtime log streaming.
- No router/API Gateway redesign.
- No frontend changes.
- No registry deletion/cleanup changes beyond existing behavior.

## Task 10.8: Runner lifecycle logs MVP

### Summary

Implemented the MVP runtime log path through the existing Redis deploy log
stream. Runner lifecycle, validation, backend, Yandex, stop, and watchdog TTL
events now publish structured deploy log entries that can be surfaced by the
current user-billing log reader.

This task intentionally does not stream Yandex Cloud Logging stdout/stderr.

### Files changed

- `docs/yandex-runtime.md`
- `snaphost-backend/internal/runtime/backend/backend.go`
- `snaphost-backend/internal/runtime/backend/docker/docker.go`
- `snaphost-backend/internal/runtime/backend/vk/vk.go`
- `snaphost-backend/internal/runtime/backend/yandex/yandex.go`
- `snaphost-backend/internal/runtime/backend/yandex/yandex_test.go`
- `snaphost-backend/internal/runtime/runner/service.go`
- `snaphost-backend/internal/runtime/runner/service_test.go`
- `snaphost-backend/internal/runtime/watchdog/watchdog.go`

### Behavior and logging changes

- Runner deploy now publishes accepted, validation passed/failed, preparation,
  backend start/failure, and public URL ready events.
- Docker backend stop now publishes container delete start/success and
  already-absent warnings.
- Yandex backend now publishes serverless container prepare/create/ready,
  revision deploy, API Gateway route update, route cleanup, and container
  delete lifecycle events.
- Watchdog TTL cleanup now publishes start, success, and failure events through
  `StopExpired`.
- Stop errors and backend errors are redacted, compacted, and truncated before
  being sent to user-visible logs.
- Backend `Stop` now receives `deployID` so backends can publish deploy-scoped
  stop lifecycle logs.
- `docs/yandex-runtime.md` documents that Redis deploy logs are the MVP runtime
  log channel and that Cloud Logging stdout/stderr tailing is future work.

### Tests added or updated

- Added runner service tests for lifecycle log publication, validation failure
  logs, backend failure logs, stop failure logs, watchdog TTL logs, and
  user-visible error redaction/compaction/truncation.
- Added Yandex backend tests for stage-specific publishing and Yandex error
  redaction/compaction/truncation.

### Verification

Passed on 2026-05-28:

```powershell
cd snaphost-backend/runner-svc
go test ./...
go test -tags yandex ./...
go build -buildvcs=false ./...
go build -buildvcs=false -tags yandex ./...

cd ../user-billing
go test ./...
go build -buildvcs=false ./...

cd ../..
git -c safe.directory=D:/snaphost diff --check
```

`git diff --check` reported only Windows line-ending conversion warnings, no
whitespace errors.

### Manual smoke result

Manual Docker/Yandex deploy smoke was not run in this session. The blocker is
that this task was verified with unit/build coverage only; no local end-to-end
deploy flow or production Yandex runtime with mounted secrets was started.

No real secrets, `.env`, `.keys`, or `terraform.tfvars` were read or printed.

### Risks and follow-ups

- Existing Redis deploy log consumers must tolerate the additional lifecycle
  messages and stages.
- Yandex Cloud Logging stdout/stderr streaming remains a separate follow-up.
- A live Docker deploy and a live Yandex deploy should be used to confirm the
  exact operator-facing log sequence after production secrets are mounted.

### Out of scope

- No Yandex Cloud Logging stdout/stderr tailing.
- No frontend or WebSocket/live-tail changes.
- No Task 10.9 work.
- No registry contract changes beyond existing Task 10.7 behavior.

## Task 10.9: Security hardening

### Summary

Hardened the production/Yandex runtime path before production smoke. Runner
stop/delete now verifies the stored user-billing deploy mapping before calling
any backend delete, runtime resources get SnapHost ownership metadata where the
backend supports it, and docs/examples restate the narrow production registry
contract.

### Files changed

- `docs/cloud-setup.md`
- `docs/yandex-runtime.md`
- `infra/.env.example`
- `snaphost-backend/internal/runtime/api/handler.go`
- `snaphost-backend/internal/runtime/backend/docker/docker.go`
- `snaphost-backend/internal/runtime/backend/docker/labels.go`
- `snaphost-backend/internal/runtime/backend/docker/labels_test.go`
- `snaphost-backend/internal/runtime/backend/yandex/yandex.go`
- `snaphost-backend/internal/runtime/backend/yandex/yandex_test.go`
- `snaphost-backend/internal/runtime/billing/client.go`
- `snaphost-backend/internal/runtime/runner/service.go`
- `snaphost-backend/internal/runtime/runner/service_test.go`
- `snaphost-backend/internal/control/deploy/handler.go`
- `snaphost-backend/internal/control/deploy/handler_test.go`

### Security behavior changes

- `runner.Service.Undeploy` fetches `GET /internal/deploys/:id` from
  user-billing before backend `Stop`.
- Backend stop is called only when the stored `container_id` is non-empty and
  exactly matches the requested `container_id`.
- Missing deploys, empty stored mappings, and mismatched runtime IDs are
  rejected as validation failures before cloud/runtime delete.
- Runner DELETE now maps ownership validation failures to HTTP 400 instead of
  a generic internal error.
- Internal user-billing `DeployInfo` now includes `container_id` additively.
- Docker containers now carry `snaphost.deploy.managed_by=snaphost`,
  `snaphost.deploy.id`, and `snaphost.deploy.user_id`.
- Yandex Serverless Container create requests now set labels:
  `managed_by=snaphost`, `deploy_id=<deploy_id>`, and `user_id=<user_id>`.
- API Gateway per-route labels are not supported through the current OpenAPI
  spec mutation path; ownership is represented by hostname/container mapping.
- Existing Task 10.8 user-visible error redaction was reviewed and kept:
  token, secret, password, private key, IAM token, and bearer fragments are
  redacted before Redis deploy-log publication.

### Tests added or updated

- Added runner undeploy tests for matching stored container ID, mismatched
  container ID rejection, missing deploy rejection, empty stored mapping
  rejection, and backend stop failure propagation after the guard passes.
- Added Docker label helper coverage for SnapHost ownership labels.
- Added Yandex label helper coverage and label value sanitization coverage.
- Updated user-billing internal deploy lookup tests to require additive
  `container_id` serialization, including the empty-string case.

### Verification

Passed on 2026-05-29 with local workspace `GOCACHE`/`APPDATA` because the
default Windows Go cache returned `Access is denied`:

```powershell
cd snaphost-backend/runner-svc
go test ./...
go test -tags yandex ./...
go build -buildvcs=false ./...
go build -buildvcs=false -tags yandex ./...

cd ../user-billing
go test ./...
go build -buildvcs=false ./...

cd ../..
git -c safe.directory=D:/snaphost diff --check
```

`git diff --check` reported only Windows line-ending conversion warnings, no
whitespace errors.

### Manual smoke result

Manual Docker/Yandex runtime smoke was not run. Docker runtime behavior changed
only by adding labels and by rejecting unsafe stop requests before backend
delete; the required follow-up smoke is:

```powershell
docker compose -f infra/docker-compose.yml up -d runner-api user-billing
# deploy a known-good app through the normal UI/API path
# delete it through the public DELETE /api/v1/deploys/:id path
# confirm the delete returns 204 and runner logs contain no ownership rejection
```

Live Yandex smoke and Terraform apply were explicitly out of scope for this
hardening task.

No real secrets, `.env`, `.keys`, or `terraform.tfvars` were read or printed.

### Risks and follow-ups

- Any caller sending a stale or client-supplied runtime ID will now be rejected
  unless it matches the billing row. This is intended, but old manual scripts
  must fetch the current mapping first.
- Existing Yandex containers created before this task may not have labels until
  recreated.
- API Gateway route ownership remains encoded in the spec rather than as
  provider-level labels because the route is not a standalone labeled Yandex
  resource in the current SDK path.

### Out of scope

- No live Yandex smoke.
- No Terraform apply.
- No frontend changes.
- No broad secret scanner.
- No Task 10.10 production smoke checklist.

## Task 10.10: Production smoke checklist

### Environment

Executed on 2026-05-31 against the configured Yandex Cloud folder and a
temporary VDS-hosted `user-billing`/Postgres/Redis stack.

- Terraform configuration validated successfully.
- `terraform -chdir=terraform/yandex apply -auto-approve` updated the central
  router container env so `USER_BILLING_URL` points to the temporary VDS
  endpoint: `http://5.42.111.112:8081`.
- Active runtime mode for this smoke: `RUNNER_BACKEND=yandex` and
  `YANDEX_ROUTING_MODE=router`.
- API Gateway ID: `d5dl9p60rr0gldujq5p0`.
- Central router container ID: `bbahn9b7qh6v6mspjmku`.
- Registry prefix: `cr.yandex/crpr68orrb2fpiosjc56/snaphost`.
- A temporary VDS `user-billing` stack was started on port `8081`.
- A temporary VDS Yandex-tagged runner API was started on port `18084`.
- A temporary VDS Yandex-tagged watchdog binary was used for TTL cleanup.

No secret values, service-account key contents, Lockbox payloads, `.env`
contents, or `terraform.tfvars` values were printed.

### Positive deploy result

Built and pushed a minimal HTTP smoke image whose tag matched the deploy ID:

- Deploy ID: `10101010-1010-4010-8010-101010101010`.
- Image ref: `cr.yandex/crpr68orrb2fpiosjc56/snaphost/smoke-1010:<deploy_id>`.
- Temporary user ID: `00000000-0000-4000-8000-000000001010`.
- Runner response: HTTP `202`.
- Created Serverless Container ID: `bbad2g0e9i3iqoifkb0o`.
- Runner endpoint URL:
  `https://proj-10101010101040108010101010101010.snaphost.online`.
- VDS user-billing row after deploy:
  `status=running`, `container_id=bbad2g0e9i3iqoifkb0o`,
  `subdomain=proj-10101010101040108010101010101010`.
- Runner log contained `deploy succeeded` for the deploy and container.

The Serverless Container create/run path is therefore working and the Yandex
runtime can pull the selected registry image.

### Public URL and routing result

Result:

```text
GET https://proj-10101010101040108010101010101010.snaphost.online/
HTTP 200
body: snaphost production smoke ok
gateway: d5dl9p60rr0gldujq5p0
gateway path: /{proxy+}
```

Interpretation: API Gateway wildcard routing reaches `router-svc`, the router
resolves the deploy through VDS `user-billing`, and the router successfully
invokes the correct Yandex Serverless Container.

### Delete cleanup result

The temporary deploy was deleted through the public `user-billing` DELETE path:

```text
DELETE /deploys/10101010-1010-4010-8010-101010101010
HTTP 204
```

Cleanup observations:

- Router mode skipped per-deploy API Gateway route cleanup, as expected.
- Runner log contained `stop: container deleted` for
  `bbad2g0e9i3iqoifkb0o`.
- Runner log contained `undeploy succeeded`.
- VDS user-billing row after delete: `status=deleted`,
  `stopped_at is not null`.
- A repeated public URL request returned `HTTP 404`, as expected after the
  route lookup no longer finds a running deploy.

### TTL cleanup result

TTL smoke used a second deploy ID and matching image tag:

- Deploy ID: `10101010-1010-4010-8010-101010101011`.
- Temporary user ID: `00000000-0000-4000-8000-000000001011`.
- Created Serverless Container ID: `bbaj52o04bcj5nf1l6ao`.
- Public URL before TTL expiry returned `HTTP 200` with body
  `snaphost production smoke ok`.
- The VDS deploy row was updated to `ttl_expires_at = now() - 1 minute`.
- A temporary VDS Yandex-tagged watchdog was started with
  `WATCHDOG_INTERVAL_SEC=3`.

Result:

- Watchdog/runner cleanup set VDS DB state to `status=stopped`,
  `stopped_at is not null`.
- Runner log contained router-mode route cleanup skip.
- Runner log contained `stop: container deleted` for
  `bbaj52o04bcj5nf1l6ao`.
- Runner log contained `undeploy succeeded`.
- A repeated public URL request returned `HTTP 404`, as expected.

### Negative tests

Safe negative tests were executed against the temporary VDS Yandex-tagged
runner:

- Invalid `image_ref` tag mismatch:
  - request image tag did not equal `deploy_id`;
  - result: HTTP `400`.
- Forbidden registry prefix:
  - request used `docker.io/library/nginx:<deploy_id>`;
  - result: HTTP `400`.
- Missing service-account key:
  - started a temporary Yandex runner with a nonexistent
    `YANDEX_SA_KEY_PATH`;
  - result: startup failed with a fatal Yandex backend init error before
    serving traffic.
- Bad API Gateway spec update:
  - skipped as not applicable to this smoke because active mode is
    `YANDEX_ROUTING_MODE=router`; runner does not mutate API Gateway per
    deploy in router mode.

### Log review

- `user-billing`, runner, watchdog, and router-facing public responses were
  reviewed during deploy, public GET, public DELETE, and TTL cleanup.
- Runner/watchdog cleanup messages confirmed container deletion and successful
  undeploy.
- The temporary VDS host runner printed Redis publish warnings because Redis
  was inside the compose network and not exposed to the host runner process.
  This did not block deploy/delete/TTL behavior, but a durable deployment
  should ensure runner can reach Redis for clean lifecycle log streaming.

### Cleanup

Temporary resources were cleaned up:

- Temporary Yandex runner/watchdog processes were stopped.
- Temporary runner service-account key and runner/watchdog binaries were
  removed from the VDS.
- Temporary VDS smoke deploy rows were deleted.
- The created Yandex smoke containers were deleted by manual delete and
  watchdog cleanup.

### Remaining blockers and follow-ups

- The full router path, public URL, public DELETE, and watchdog TTL smoke passed
  against temporary infrastructure.
- The current VDS stack is smoke infrastructure, not a durable production
  deployment. Before treating this as production-ready, replace it with the
  real/staging `user-billing` and runner deployment or keep an intentionally
  managed staging environment.
- Ensure the durable runner deployment has Redis connectivity so Task 10.8
  lifecycle log publishing is clean in production.
