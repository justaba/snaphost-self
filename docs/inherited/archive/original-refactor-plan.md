# Snaphost Backend: Refactoring Tasks (Revised)

Status: Archived original plan
Superseded by: `docs/tasks/README.md` and current task documents

You are working on the **Snaphost** monorepo — an instant-deployment platform written in Go (microservices) + React. This document supersedes any previous task list. Read it fully before starting.

## Critical Context

The repo is in **pre-MVP state**:
- No git history (project gets pushed once it reaches MVP).
- The application **works locally** on `RUNNER_BACKEND=docker` — full deploy flow (clone → build → push → run → URL) is verified manually after each change.
- The `runner-svc/internal/backend/yandex/` package **does not compile** against any current SDK version — it was written against a force-pushed pseudo-version that no longer exists. This package is "future production" code; it has never run end-to-end.
- The `builder-svc/internal/build/yandex_iam.go` package has its own duplicate JWT-signing logic; this code path IS exercised whenever builder-svc pushes to Yandex Container Registry (planned, not yet wired in production).

## Architectural Invariant: Pluggable Backend (NON-NEGOTIABLE)

The platform supports multiple cloud providers via the `runner-svc/internal/backend.Backend` interface. Current backends: `docker` (working, used in dev), `yandex` (broken, future prod), `vk` (skeleton). Selection via `RUNNER_BACKEND` env var.

**Rules for every task in this list:**
1. Provider-specific logic stays in the corresponding `internal/backend/<provider>/` package. Never leak it into common code.
2. If a task needs functionality that should work across providers, the `backend.Backend` interface gets extended, and EVERY backend (including stubs) gets an implementation — even if it's `return errors.New("not supported by this backend")`.
3. Configuration that's provider-specific (e.g. `YANDEX_REGISTRY_URL`, `VK_PROJECT_ID`) lives in the corresponding backend's config namespace, never in generic config.
4. Common configuration that varies by provider (e.g. allowed registry prefixes for image validation) is exposed as `[]string` or similar, and each backend contributes its own values.

This invariant exists because the user explicitly designed multi-provider support and intends to switch dynamically.

## Working Principles

1. **One task = one PR-sized commit.** Tasks done in the order listed below. No task started until previous is reviewed and approved.
2. **No drive-by edits.** Unrelated issues get a `// TODO(refactor):` comment and are left alone.
3. **Docker backend must keep working after every commit.** This is your acceptance smoke test. After each task: `make dev`, deploy a known repo through the UI, see the URL serve traffic. If it breaks, revert.
4. **Tests required for logic changes; not required for pure refactoring.** When fixing a bug, a failing test that reproduces it goes in first.
5. **`make lint` and `make build` (without `-tags yandex`) green after every task.** Yandex-tagged build can stay broken until the rewrite task.
6. **Don't break the saga state machine.** State transitions: `pending → reserved → building → built → provisioning → running` (or compensating). Changes need explicit justification.
7. **Don't reorder middleware in `api-gateway/main.go`.** Order is documented and load-bearing.
8. **Preserve external API shapes** (request/response JSON for `/api/v1/*` and `/internal/*`) unless a task explicitly says otherwise.
9. **When stuck, stop and ask.** Especially when SDK API doesn't match what code expects, when migrations don't apply cleanly, when tests fail in unexpected places. Don't paper over with TODOs.

---

## Task 0 — Isolate broken Yandex backend behind build tag

**Problem:** `runner-svc/internal/backend/yandex/` doesn't compile. This blocks `make build` and any task that touches runner-svc. Since the Docker backend is what actually runs in dev, isolating Yandex behind a build tag unblocks everything else.

**Goal:** `go build ./...` succeeds in default mode. `go build -tags yandex ./...` is allowed to fail (we'll fix it in a dedicated rewrite task later).

**Steps:**

1. Add to the top of every `.go` file in `runner-svc/internal/backend/yandex/` (currently `yandex.go`, `iam.go`, `gateway_spec.go`):
   ```go
   //go:build yandex
   // +build yandex
   ```
   The blank line after the build tag is required.

2. Find the backend selection switch. Likely in `runner-svc/cmd/api/main.go` or `runner-svc/internal/backend/backend.go`. Look for code branching on `cfg.RunnerBackend`. There will be a direct import of the `yandex` package.

3. Replace the direct import with a build-tag-gated indirection. Create two new files in the same package as the switch:

   **`internal/backend/yandex_enabled.go`:**
   ```go
   //go:build yandex
   // +build yandex

   package backend // or whichever package the switch lives in

   import (
       "snaphost/runner-svc/config"
       "snaphost/runner-svc/internal/backend/yandex"
       "snaphost/runner-svc/internal/logs"
       "go.uber.org/zap"
   )

   func newYandexBackend(cfg *config.Config, pub logs.Publisher, log *zap.Logger) (Backend, error) {
       return yandex.NewYandexBackend(cfg, pub, log)
   }
   ```

   **`internal/backend/yandex_stub.go`:**
   ```go
   //go:build !yandex
   // +build !yandex

   package backend

   import (
       "errors"
       "snaphost/runner-svc/config"
       "snaphost/runner-svc/internal/logs"
       "go.uber.org/zap"
   )

   func newYandexBackend(_ *config.Config, _ logs.Publisher, _ *zap.Logger) (Backend, error) {
       return nil, errors.New("yandex backend not compiled in: rebuild with -tags yandex")
   }
   ```

4. In the switch, replace `case "yandex": return yandex.NewYandexBackend(...)` with `case "yandex": return newYandexBackend(cfg, pub, log)`. Adjust to match actual signatures used.

5. **Verify the full backend matrix:**
   ```bash
   cd snaphost-backend
   for svc in api-gateway builder-svc runner-svc user-billing ai-orchestrator; do
     echo "=== $svc default ==="
     (cd $svc && go build ./...) || echo "FAIL"
   done
   echo "=== runner-svc with -tags yandex ==="
   (cd runner-svc && go build -tags yandex ./...) || echo "FAIL (expected for now)"
   ```
   Default builds must all pass. The `-tags yandex` build is expected to fail with the SDK errors we already know about — that's OK, document it in NOTES.

6. **Smoke test:** `make dev`, wait for stack to come up, deploy a known-good repo through UI, verify URL serves traffic. If broken, revert.

**Acceptance:**
- All five services build in default mode.
- Docker deploy flow still works end-to-end.
- `RUNNER_BACKEND=yandex` at runtime returns a clear error rather than crashing on import.
- `REFACTOR_NOTES.md` created at repo root with a section "Task 0: Yandex backend isolated behind build tag" explaining what was done and why.

**Out of scope:** Fixing Yandex SDK migration. That's a dedicated future task explicitly listed at the end of this document.

---

## Task 1 — Create `shared/yandexauth/` and migrate builder-svc

**Problem:** `builder-svc/internal/build/yandex_iam.go` reimplements JWT signing, IAM token exchange, RSA key parsing, and token caching from scratch. This is the same logic that should live in the Yandex SDK. Once shared/yandexauth/ exists, both builder-svc (now) and runner-svc (later, after Yandex rewrite) will use it.

**Goal:** Single source of truth for Yandex authentication, used by builder-svc. runner-svc keeps its current state until the Yandex rewrite task.

### Step 1.1 — SDK reconnaissance

Before writing any code, evaluate which SDK version to pin against. The previous pseudo-version pin is dead; we need to choose between `v0.31.0` (latest v0 line) and `v2.latest` (separate module path).

Set up a sandbox:
```bash
mkdir /tmp/yc-recon && cd /tmp/yc-recon
go mod init recon
```

For each candidate version, attempt to add it and read the package source:
```bash
go get github.com/yandex-cloud/go-sdk@v0.31.0
# Then explore ~/go/pkg/mod/github.com/yandex-cloud/go-sdk@v0.31.0/
```

For each version, fill in this table and report back:

| Question | v0.31.0 | v2.latest |
|---|---|---|
| Package path for SDK builder | `github.com/yandex-cloud/go-sdk` | ? |
| Package path for `iamkey` | ? | ? |
| Function for reading authorized key from JSON | ? | ? |
| Function for building credentials from key | ? | ? |
| Method to get current IAM token from SDK | ? | ? |
| Return type of token method (struct field name) | ? | ? |
| Does serverless/containers package exist? | yes/no | yes/no |
| Does serverless/apigateway package exist? | yes/no | yes/no |
| Operation result extraction pattern | ? | ? |

Don't write code. Send the table. **I will choose the version.**

### Step 1.2 — Implement `shared/yandexauth/`

After version chosen, create:

**`shared/yandexauth/sdk.go`:**

```go
// Package yandexauth provides Yandex Cloud authentication helpers
// shared between services that need to talk to Yandex APIs.
//
// The SDK manages IAM token lifecycle internally (signs JWT with the
// authorized key, exchanges for a 12h IAM token, refreshes before
// expiry). Callers should construct one *ycsdk.SDK at startup and
// pass it down — do not create per-request.
package yandexauth

import (
    "context"
    "fmt"
    // imports per chosen SDK version
)

// NewSDK builds a Yandex Cloud SDK authenticated with a service account
// authorized key from the given JSON file path.
func NewSDK(ctx context.Context, keyPath string) (*ycsdk.SDK, error) {
    // implementation per chosen SDK version
}

// IAMToken returns the SDK's current valid IAM bearer token. Use this
// when passing the token to non-SDK consumers (e.g. BuildKit auth provider,
// docker login). The SDK refreshes the token internally; consecutive
// calls are cheap.
func IAMToken(ctx context.Context, sdk *ycsdk.SDK) (string, error) {
    // implementation per chosen SDK version
}
```

Add a smoke test `shared/yandexauth/sdk_test.go` that:
- Generates an RSA-2048 key in-memory (`rsa.GenerateKey`).
- Writes a fake authorized-key JSON to a temp file.
- Calls `NewSDK` and verifies it returns a non-nil SDK without panicking.
- **Does not make network calls.** The IAM API exchange will lazily happen when the SDK first needs a token; the test just verifies construction.

### Step 1.3 — Migrate builder-svc

In `builder-svc/internal/build/yandex_iam.go`:

1. Replace the manual JWT signing, RSA parsing, HTTP client, and token cache with a `*ycsdk.SDK` field obtained from `yandexauth.NewSDK()`.
2. Keep the `session.Attachable` interface (BuildKit needs it).
3. The `Credentials()` method becomes:
   ```go
   func (a *yandexIAMAuth) Credentials(ctx context.Context, req *auth.CredentialsRequest) (*auth.CredentialsResponse, error) {
       if !strings.Contains(req.Host, "cr.yandex") {
           return &auth.CredentialsResponse{}, nil
       }
       tok, err := yandexauth.IAMToken(ctx, a.sdk)
       if err != nil {
           return nil, err
       }
       return &auth.CredentialsResponse{Username: "iam", Secret: tok}, nil
   }
   ```
4. The constructor signature changes to take SDK instead of key path:
   ```go
   func newYandexIAMAuth(sdk *ycsdk.SDK, log *zap.Logger) (session.Attachable, error)
   ```
   Update `builder-svc/internal/build/buildkit.go::NewBuilder` to construct SDK from `cfg.YandexSAKeyPath` via `yandexauth.NewSDK()` and pass it down.
5. Delete the now-unused JWT/RSA/HTTP code from `yandex_iam.go`. The file should shrink by ~100 lines.

**Acceptance:**
- `go build ./...` (default mode) passes for all five services.
- `make test` passes.
- builder-svc no longer imports `crypto/rsa`, `crypto/x509`, `encoding/pem`, or `golang-jwt/jwt/v5` directly (the SDK handles those internally now).
- `shared/yandexauth/sdk_test.go` passes.
- Docker deploy smoke test still works (this task doesn't touch the docker path, but verify anyway).
- `REFACTOR_NOTES.md` updated with task summary, chosen SDK version, and rationale.

**Out of scope:**
- runner-svc/internal/backend/yandex/ stays as-is (build-tagged off). Migration of that package is the dedicated future "Yandex backend rewrite" task.
- Docker config / static credential mode in `buildkit.go` — leave it alone.

---

## Task 2 — Validate `image_ref` in runner-svc (provider-agnostic)

**Problem:** `runner-svc/api/handler.go::Deploy` accepts any `image_ref` from any caller knowing the webhook secret. No defense against compromised internal services or bugs sending wrong values.

**Goal:** Cross-check that the deploy request is internally consistent against user-billing's record, and that the image lives in an allowed registry. Provider-agnostic.

**Steps:**

1. Confirm whether `GET /internal/deploys/:id` exists on user-billing. Look in `user-billing/routes/` and `user-billing/internal/deploy/`. If yes — check what it returns. If no — add it. Returns:
   ```json
   {"deploy_id": "...", "user_id": "...", "status": "...", "image_ref": "..."}
   ```
   Use existing `deployRepo.Get` or equivalent. Webhook-secret-protected like other internal endpoints.

2. In `runner-svc/internal/billing/client.go`, add `GetDeploy(ctx, deployID) (*DeployInfo, error)`.

3. Add to `runner-svc/config/config.go`:
   ```go
   // AllowedRegistryPrefixes lists registry URL prefixes that image_ref values
   // are permitted to start with. Each backend contributes its own values
   // (e.g. "cr.yandex/<id>/" for yandex, "registry:5000/" for docker dev).
   // Configured via REGISTRY_ALLOWED_PREFIXES (comma-separated).
   AllowedRegistryPrefixes []string

   // StrictImageValidation toggles the user_id and status checks against
   // user-billing. Must be true in prod. Set false only for local dev.
   StrictImageValidation bool
   ```
   Defaults: `StrictImageValidation` true in prod env, `AllowedRegistryPrefixes` empty (no validation if not set, with a warning log at startup).

4. Update `infra/.env.example` with the new variables, including a worked example for the dev case.

5. In `runner-svc/internal/runner/service.go::Deploy`, add validation before `s.backend.Run()`:
   ```go
   if err := s.validateDeployRequest(ctx, req); err != nil {
       return nil, &ValidationError{Err: err}
   }
   ```
   Where `validateDeployRequest` checks (in order):
   - `image_ref` matches at least one of `cfg.AllowedRegistryPrefixes` (skip if list empty + log warning).
   - Tag portion (after last `:`) equals `req.DeployID`.
   - If `StrictImageValidation`: `billing.GetDeploy(deployID).UserID == req.UserID`.
   - If `StrictImageValidation`: `billing.GetDeploy(deployID).Status == "built"`.
   - If `StrictImageValidation` and deploy already in `running` state: return `ErrAlreadyRunning` (caller maps to 409).

6. Define `ValidationError` and `ErrAlreadyRunning` types in `runner-svc/internal/runner/errors.go`. The HTTP handler maps `ValidationError` → 400, `ErrAlreadyRunning` → 409.

7. Tests in `runner-svc/internal/runner/service_test.go` (new file or extend existing). Mock the billing client. Cover:
   - Happy path: passes validation, calls backend.Run.
   - Wrong registry prefix: returns ValidationError, doesn't call backend.
   - Tag mismatch: returns ValidationError.
   - User ID mismatch (strict mode): returns ValidationError.
   - Wrong status (strict mode): returns ValidationError.
   - Already running: returns ErrAlreadyRunning.
   - Strict mode off: skips user/status checks, validates only registry+tag.

**Acceptance:**
- All tests pass.
- Docker deploy smoke test works with appropriate `REGISTRY_ALLOWED_PREFIXES` in `.env`.
- HTTP responses use `errResponse` shape from existing handler code.
- `REFACTOR_NOTES.md` updated.

**Out of scope:** Validating the image content itself (e.g. signature verification with cosign). Future hardening.

---

## Task 3 — `clone.isWithin` correctness

**Problem:** `builder-svc/internal/clone/cloner.go::isWithin` uses fragile `rel[0] != '.'` check. Conflates `..` with hidden directories like `.config`.

**Steps:**

1. Replace with canonical implementation:
   ```go
   func isWithin(child, parent string) bool {
       absChild, err := filepath.Abs(child)
       if err != nil { return false }
       absParent, err := filepath.Abs(parent)
       if err != nil { return false }
       rel, err := filepath.Rel(absParent, absChild)
       if err != nil { return false }
       if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
           return false
       }
       return true
   }
   ```

2. Add `builder-svc/internal/clone/path_test.go` with table-driven tests. Cases (minimum): identical paths, child of parent, sibling, parent of child, completely unrelated, paths with `..` segments, paths with `.` segments, hidden file inside parent (`.config`), hidden file outside parent.

**Acceptance:** Test file with ≥10 cases. All pass. Docker smoke test still works.

---

## Task 4 — Fix DNS rebinding (TOCTOU) in clone validation

**Problem:** `builder-svc/internal/clone/url_validator.go::ValidateRepoURL` resolves hostname, checks public IP, returns OK. Then `git.PlainCloneContext` resolves DNS again — attacker with controlled DNS can return public IP first, `169.254.169.254` (cloud metadata) second, leaking IAM tokens of any cloud-attached SA.

**Goal:** IP that passes validation is the IP that gets connected to. Plus better filtering.

**Steps:**

1. Refactor `ValidateRepoURL` to return `(*ValidatedURL, error)` where:
   ```go
   type ValidatedURL struct {
       URL  string  // original
       Host string  // hostname
       IP   net.IP  // the validated IP to pin connections to
   }
   ```

2. Strengthen the IP filter. Reject any of:
   - `IsPrivate()`, `IsLoopback()`, `IsLinkLocalUnicast()`, `IsMulticast()`, `IsUnspecified()`.
   - Specific cloud metadata IPs: `169.254.169.254`, `fd00:ec2::254`.
   - IPv4-mapped IPv6 of the above (use `IP.To4()` then re-check).

3. Pick the first IP that passes filtering. If multiple, prefer IPv4. If none pass, return error.

4. Modify `clone.Cloner.Clone` to take `*ValidatedURL` instead of `URL string`. Update `pipeline/runner.go` to pass it.

5. Build a custom HTTP transport that pins connections:
   ```go
   import "github.com/go-git/go-git/v5/plumbing/transport/client"
   import githttp "github.com/go-git/go-git/v5/plumbing/transport/http"

   pinnedHost := validatedURL.Host
   pinnedIP := validatedURL.IP.String()
   dialer := &net.Dialer{Timeout: 30 * time.Second}

   transport := &http.Transport{
       DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
           host, port, err := net.SplitHostPort(addr)
           if err != nil {
               return nil, err
           }
           if host != pinnedHost {
               return nil, fmt.Errorf("clone: unexpected host %q (pinned to %s)", host, pinnedHost)
           }
           return dialer.DialContext(ctx, network, net.JoinHostPort(pinnedIP, port))
       },
       TLSHandshakeTimeout: 15 * time.Second,
   }

   httpClient := &http.Client{Transport: transport}
   client.InstallProtocol("https", githttp.NewClient(httpClient))
   ```
   (Verify exact go-git API for installing custom HTTP client; the package layout has changed between versions. If different, adjust.)

6. **Tear down the custom protocol after clone**, otherwise it leaks into other clones running concurrently. Use a sync.Mutex around the whole operation, OR (better) use go-git's `clone.Options.HTTPClient` field if available.

7. Remove `ValidateRepoURL` call from `builder-svc/api/handler.go::Build`. Keep ONLY syntactic checks there:
   - HTTPS scheme.
   - Path matches `/owner/repo` shape.
   - Host name length under 253 chars.
   - URL length under 2000 chars.

   Full validation (with DNS) happens only in the worker, immediately before clone. This shrinks the TOCTOU window from "queue depth" (potentially minutes) to "single function call" (microseconds).

8. Tests:
   - DNS-rebinding resistance: mock a `net.Resolver` that returns `8.8.8.8` first, `169.254.169.254` second. Verify the pinned dialer rejects the second.
   - Valid URL still works: clone a tiny public repo (or mock the entire transport).
   - All filter cases: IPv4 private, IPv6 link-local, metadata IP, etc. each rejected.

**Acceptance:**
- Tests pass.
- Docker smoke test works (deploy a real GitHub repo).
- The TODO is left in code:
  ```go
  // TODO(infra): defense-in-depth — run builder-worker in a network namespace
  // with no route to 169.254.0.0/16 and fe80::/10 to make metadata IPs
  // unreachable even if validation has a bug.
  ```

---

## Task 5 — Transient vs permanent errors in builder worker

**Problem:** `builder-svc/cmd/worker/main.go` returns `nil` to `q.Consume` on every error, ack-ing transient failures (Redis hiccup, billing service blip) as if they were permanent. User loses deploys to network blips.

**Steps:**

1. Add `builder-svc/internal/pipeline/errors.go`:
   ```go
   package pipeline

   import "errors"

   // ErrTransient marks errors caused by external systems (network, db,
   // upstream service) where retry is the right action.
   var ErrTransient = errors.New("transient error")

   // ErrPermanent marks errors caused by user input or environment
   // (validation, build failure, scan failure, hallucinated Dockerfile)
   // where retry won't help.
   var ErrPermanent = errors.New("permanent error")

   func Transient(err error) error {
       if err == nil {
           return nil
       }
       return fmt.Errorf("%w: %w", ErrTransient, err)
   }

   func Permanent(err error) error {
       if err == nil {
           return nil
       }
       return fmt.Errorf("%w: %w", ErrPermanent, err)
   }
   ```

2. In `pipeline/runner.go::executePipeline`, classify errors:
   - URL validation, Dockerfile validation, AI hallucinated Dockerfile, BuildKit solve failures, Trivy CVE findings → `Permanent`.
   - Status reporting (HTTP to billing), AI orchestrator HTTP, IAM token fetch (when added), Redis publishing → `Transient`.
   - Clone errors are tricky: network errors during clone are transient (retry might work); auth errors and "repo not found" are permanent. Examine the `git.ErrXxx` sentinel errors and classify accordingly.

3. In `cmd/worker/main.go`:
   ```go
   if err := runner.Run(ctx, job); err != nil {
       if errors.Is(err, pipeline.ErrTransient) {
           log.Warn("transient pipeline error, returning to queue",
               zap.String("deploy_id", job.DeployID), zap.Error(err))
           return err  // do NOT ack; Redis Streams retries
       }
       log.Error("permanent pipeline failure",
           zap.String("deploy_id", job.DeployID), zap.Error(err))
       return nil  // ack, do not retry; failure already reported to billing
   }
   ```

4. In `internal/queue/redis_stream.go::Consume`, ensure non-nil return does NOT call `XAck`. Add max-retry cap: read `XPENDING` delivery count, if ≥ 5, log "exhausted retries" and ack anyway (move to dead-letter list — implement only if simple, otherwise just ack with a warning log).

5. Tests:
   - Mock billing HTTP client to fail twice then succeed: job retries and eventually completes.
   - Mock validation to fail: job ack'd immediately, no retries.

**Acceptance:**
- Tests pass.
- Docker smoke test works (this is mostly internal logic).
- `REFACTOR_NOTES.md` updated.

---

## Task 6 — Subdomain collision fix

**Problem:** `runner-svc/internal/runner/service.go::generateSubdomain` uses 8 hex chars = 32 bits. Birthday collisions at ~65k deploys. User could see another user's app.

**Steps:**

1. Choose strategy. Two options, pick one and document choice:
   - **A:** Use full UUID (32 hex). Subdomains become `proj-<32-hex>.example.com`. Long but trivially unique.
   - **B:** Add `subdomain VARCHAR(20)` UNIQUE column to `deploys` table. Compute as `proj-<8-hex>` first try; on conflict, retry with random suffix up to 3 times. Subdomain stored at saga step `StepReserved` time, deterministic per deploy after that.

   Default: **A** unless team explicitly wants short subdomains.

2. For option A:
   - Update `generateSubdomain` to strip dashes and use full UUID.
   - Update tests.

3. For option B:
   - New migration in `user-billing/db/migrations/`.
   - `deploy.Repository` gets a `ReserveSubdomain(deployID, subdomain)` method.
   - Saga generates subdomain at `StepReserved`, persists, runner-svc reads from billing.

4. **Backward compatibility:** Existing deploys (during rollout) keep their 8-char subdomains. Don't migrate old data. Add a code comment noting that the function tolerates legacy short subdomains.

**Acceptance:**
- Tests for new logic.
- Docker smoke test works — deploy a repo, verify URL format matches new scheme.
- `REFACTOR_NOTES.md` updated with chosen option and rationale.

---

## Task 7 — Trivy `--insecure` flag is conditional

**Problem:** `builder-svc/internal/scan/trivy.go` always passes `--insecure`. For Yandex Container Registry (TLS), this disables certificate validation in prod.

**Steps:**

1. In `builder-svc/config/config.go` add:
   ```go
   // RegistryInsecure disables TLS verification when scanning images.
   // Only relevant for local/dev registries without TLS. In prod (cr.yandex
   // and similar) leave this false.
   RegistryInsecure bool
   ```
   Loaded from `REGISTRY_INSECURE` env var, default `false`.

2. Auto-detect for safety: if `RegistryURL` matches one of `localhost`, `127.0.0.1`, `registry:`, set `RegistryInsecure = true` with a log message.

3. In `scan/trivy.go::Scan`, conditionally append `--insecure`:
   ```go
   args := []string{"image", "--format", "json", ...}
   if s.cfg.RegistryInsecure {
       args = append(args, "--insecure")
   }
   args = append(args, imageRef)
   ```

4. Update `infra/.env.example` to document the flag.

**Acceptance:** Docker smoke test works (auto-detect kicks in for local registry). `REFACTOR_NOTES.md` updated.

---

## Task 8 — Trivy scan before push (or cleanup on scan failure)

**Problem:** `pipeline/runner.go` builds+pushes to registry, THEN scans. Failed scan leaves vulnerable image in registry, where it can still be pulled.

**Goal:** Either scan before push, or delete from registry on scan failure. Provider-agnostic.

**Decision criteria:** This task is more invasive than option A vs option B suggests. Before starting, check what BuildKit version is in `infra/docker-compose.yml` (`moby/buildkit` image tag). Check the BuildKit docs for that version:
- Does it support `client.ExporterOCI` to a local file? If yes → option A is feasible.
- Is there a way to do `solve` with `--output type=oci,dest=<path>` then a separate `--output type=image,name=<ref>,push=true`? If yes → option A.

Report findings before writing code. **I will pick A or B.**

### Option A — scan before push (preferred if feasible)

1. Modify `BuildOptions`: add `OutputMode` enum (`OutputModePushImage` | `OutputModeLocalTar`).
2. `Build()` switches on output mode, configures appropriate exporter.
3. `Scan()` accepts either an image ref or local tarball path. If `.tar` extension, runs `trivy image --input <path>`.
4. `pipeline/runner.go::executePipeline`:
   - Build with `OutputModeLocalTar` → tarball at `{ContextDir}/.snaphost-image.tar`.
   - Scan tarball.
   - On scan pass: second BuildKit call with `OutputModePushImage`. Cache hits, fast.
   - Cleanup tarball regardless of outcome.

### Option B — cleanup on scan failure

1. Add `internal/registry/` package with `Client` interface and provider-specific implementations.
   ```go
   type Client interface {
       Delete(ctx context.Context, imageRef string) error
   }
   ```
2. Implementations:
   - `internal/registry/yandex.go` (build-tagged off, since Yandex backend isn't built): uses SDK to delete from cr.yandex.
   - `internal/registry/docker_v2.go`: uses HTTP DELETE against `/v2/<name>/manifests/<tag>` (Docker Registry API v2).
3. In `pipeline/runner.go`, on `scan.ErrCriticalVulnerability` with gate enabled:
   ```go
   if delErr := r.Registry.Delete(ctx, imageRef); delErr != nil {
       log.Error("cleanup of vulnerable image failed", zap.Error(delErr))
   }
   ```

**Acceptance:**
- Test that builds an image with a known CRITICAL CVE (use `nginx:1.10` or similar old tag with documented CVEs), verifies image not pullable from registry after pipeline fails.
- Happy path test (no CVEs) still works.
- Docker smoke test works.

**Tricky bit:** The current `infra/docker-compose.yml` uses a local Docker registry without TLS. Option B's HTTP DELETE needs to handle that. Trivy + this need to be consistent on TLS handling.

---

## Future Tasks (NOT in this round)

These are documented for context, do not start them without explicit instruction:

### Task 10 — First production runtime adapter: Yandex Cloud

Goal: implement Yandex Cloud as the first production runtime adapter, not as a
platform-wide dependency. SnapHost core remains provider-agnostic;
`RUNNER_BACKEND=yandex` is the initial concrete cloud adapter. User projects
are built into Yandex Container Registry images and deployed as Yandex
Serverless Containers behind Yandex API Gateway.

This work must stay provider-agnostic at the service boundary:
Yandex-specific logic lives under `runner-svc/internal/backend/yandex/`
or Yandex-specific registry clients. Docker backend must keep working
after each subtask, and future production adapters such as AWS/GCP/VK should be
able to plug into the same backend/provider boundaries.

#### Task 10.0 — Production auth and env contract

Document the production contract before changing code:

- Backend services receive service-account authorized-key JSON files,
  not pre-created IAM tokens.
- `builder-svc` receives the builder service-account key and uses it only
  to push images to Yandex Container Registry.
- `runner-svc` receives the runner service-account key and uses it only
  to manage Serverless Containers, API Gateway routes, image pulls, and
  logs.
- Bootstrap/admin Terraform credentials never get mounted into production
  backend containers.
- Terraform outputs map explicitly to backend env vars:
  `YANDEX_SA_KEY_PATH`, `YANDEX_FOLDER_ID`, `YANDEX_RUNNER_SA_ID`,
  `YANDEX_API_GATEWAY_ID`, `YANDEX_REGISTRY_URL`, and `REGISTRY_URL`.

Deliverable: update `docs/cloud-setup.md` or create
`docs/yandex-runtime.md` with the secret mounting and env mapping.

#### Task 10.1 — Terraform IAM role audit

Review `terraform/yandex/service_accounts.tf` for least privilege:

- `snaphost-builder`: registry image pusher only.
- `snaphost-runner`: Serverless Containers editor, API Gateway editor,
  registry image puller, `iam.serviceAccounts.user`, log reader/invoker
  roles as needed.
- No production runtime service account has IAM admin, folder admin,
  billing admin, DNS admin, or permission to manage unrelated resources.

Deliverable: Terraform roles and outputs match the runtime contract.

#### Task 10.2 — Make Yandex backend compile

Fix `runner-svc/internal/backend/yandex/` against the chosen SDK line
(`github.com/yandex-cloud/go-sdk@v0.31.0` unless a new investigation
changes that decision).

Acceptance:

- `go build ./...` without tags remains green.
- `go build -tags yandex ./...` in `runner-svc` is green.
- Yandex code remains isolated behind the `yandex` build tag.

Do not implement new behavior in this task unless required to make the
existing package compile cleanly.

#### Task 10.3 — Runner auth via `shared/yandexauth`

Wire `runner-svc` Yandex backend to `shared/yandexauth`:

- Build one SDK at startup from `YANDEX_SA_KEY_PATH`.
- Remove any remaining hand-rolled IAM/JWT/token logic from runner.
- Validate required Yandex env vars on startup when
  `RUNNER_BACKEND=yandex`.

Acceptance: runner can construct authenticated SDK clients from an
authorized-key JSON file; no manual `yc iam create-token` is needed for
deploys.

#### Task 10.4 — Minimal Yandex deploy happy path

Implement `Backend.Run` for the Yandex backend:

1. Receive the validated internal deploy request from `runner-svc`.
2. Create or update a Yandex Serverless Container named from `deploy_id`
   (`proj-<deploy_id>` or the existing normalized subdomain).
3. Use the image built by `builder-svc` in Yandex Container Registry.
4. Apply configured env, memory, CPU, timeout, and app port.
5. For the 10.4 MVP, update the Yandex API Gateway proxy route enough for
   one smoke deploy to serve traffic.
6. Return the public URL to `user-billing`.

Acceptance: one known public repo deploys end-to-end to Yandex and serves
traffic at `https://<subdomain>.<domain>`. Full concurrent multi-deploy
host routing is explicitly deferred to Task 10.6.

#### Task 10.5 — Stop/delete/TTL lifecycle

Implement Yandex cleanup paths:

- `DELETE /internal/deploys/:id` removes or disables the deploy's
  Serverless Container and removes/restores the current API Gateway
  route/proxy mapping used by the Yandex backend.
- `runner-watchdog` works with the Yandex backend for expired deploys.
- Cloud resource IDs or stable names are stored/derived so delete/update
  never depends on user-supplied arbitrary Yandex resource IDs.

Acceptance: manual delete and TTL expiry both remove Yandex runtime
resources and update `user-billing` state correctly.

#### Task 10.6 — Safe API Gateway spec management

Treat API Gateway spec mutation as its own risky subtask:

- Start from the 10.4 state: the MVP proxy route is sufficient for one
  smoke deploy, but is not the final multi-tenant routing model.
- Decide and document the production routing shape: host-based routing in one
  API Gateway if Yandex supports the required behavior, per-deploy gateway,
  or another explicit routing layer.
- Read the current spec.
- Add/remove only the route belonging to the target deploy.
- Preserve all unrelated routes.
- Unit-test route add/remove/merge behavior without SDK calls.
- Handle concurrent updates with retry or another explicit strategy.
- Also fix the Task 10.5 lifecycle accounting follow-up: when a deploy is
  stopped by runner-svc/watchdog, user-billing must persist `stopped_at`
  instead of only changing `status='stopped'`. Prefer a small explicit
  repository/API transition such as `MarkStopped`, or make the existing
  internal status update set `stopped_at = coalesce(stopped_at, now())` for
  `status='stopped'` without changing unrelated statuses.

Acceptance: two deploy route changes cannot accidentally wipe each other
or reset unrelated gateway spec sections, and two live deploys can serve
different subdomains concurrently. TTL/watchdog stop must leave the deploy row
with `status='stopped'` and non-null `stopped_at`.

#### Task 10.6c — Router infra wiring and live Yandex smoke

Complete the central-router runtime path introduced after Task 10.6:

- Deploy `router-svc` as the static wildcard runtime target for Yandex API
  Gateway.
- Keep `runner-svc` in `YANDEX_ROUTING_MODE=router`, so deploys create/delete
  Serverless Containers but do not mutate API Gateway routes per deploy.
- Add the minimum Terraform/API Gateway wiring needed for wildcard traffic to
  reach `router-svc`.
- Document every required runtime env var and Terraform output mapping.
- Keep secrets out of Terraform state, docs, logs, and commits.

Acceptance:

- `router-svc` image builds from the documented Docker context.
- API Gateway wildcard runtime traffic reaches `router-svc`.
- Two live Yandex deploys with different subdomains serve different response
  bodies concurrently.
- Deleting or expiring one deploy removes its container and does not break the
  other deploy.
- A foreign host that reaches the router is rejected before user-billing lookup.
- Smoke-test results are recorded in `REFACTOR_NOTES.md` with exact dates,
  commands, URLs/domains redacted where needed, and observed outcomes.

Out of scope:

- Task 10.7 registry contract changes.
- Task 10.8 runtime log streaming.
- WebSocket, large upload, and long-streaming proxy support.

#### Task 10.7 — Registry end-to-end integration

Verify builder and runner use the same Yandex registry contract:

- `builder-svc` pushes to `cr.yandex/<registry_id>/snaphost/...`.
- `runner-svc` accepts only the Yandex registry prefix in production
  `REGISTRY_ALLOWED_PREFIXES`.
- `REGISTRY_AUTH_MODE=yandex_iam`.
- `REGISTRY_INSECURE=false`.
- `SCAN_FAIL_ON_CRITICAL=true`.

Acceptance: image refs produced by builder pass runner strict validation
and are pullable by the Yandex runtime.

#### Task 10.8 — Runtime logs

MVP:

- Keep build logs as-is through Redis.
- Publish runner lifecycle events to Redis logs: container created,
  API Gateway route added, public URL ready, and failure reasons.

Later:

- Integrate Yandex Cloud Logging for runtime stdout/stderr streaming.

Acceptance for MVP: user can see deploy progress and Yandex lifecycle
failures even before live runtime log streaming is implemented.

#### Task 10.9 — Security hardening

Before production use:

- Ensure service-account keys are never logged, committed, or returned in
  API responses.
- All Yandex resources created by SnapHost are labeled/metadata-tagged
  with `managed_by=snaphost`, `deploy_id`, and `user_id` where supported.
- Delete/update verifies ownership from `user-billing` and stored mapping,
  not from client-provided cloud IDs.
- Strict image validation is enabled.
- Production allowed registry prefixes include only the Yandex registry.

Acceptance: compromise blast radius is limited to SnapHost-managed runtime
resources, not the whole Yandex folder.

#### Task 10.10 — Production smoke checklist

Run and record these scenarios:

1. Deploy a simple public repo.
2. Open `https://<subdomain>.<domain>`.
3. Confirm the Serverless Container exists in Yandex Console.
4. Confirm the expected API Gateway route/proxy mapping exists.
5. Delete the deploy and confirm container/route cleanup.
6. Let a deploy expire and confirm watchdog cleanup.
7. Negative tests: invalid `image_ref`, forbidden registry prefix,
   missing service-account key, bad gateway spec update.
8. Review logs for all involved services after each smoke test.

Acceptance: checklist results are recorded in `REFACTOR_NOTES.md`.

#### Task 10 backlog follow-ups

- Yandex Container Registry delete implementation for vulnerable image
  cleanup after failed Trivy scans.
- Full runtime stdout/stderr streaming from Yandex Cloud Logging.
- Workload Identity or metadata-based credentials if production hosting
  moves fully into Yandex-managed compute and can avoid static key files.

Do not bundle these follow-ups into the minimal first-adapter runtime path.

### Future Task — Provider abstraction audit

Once Yandex rewrite is done, audit the `backend.Backend` interface for any provider-specific assumptions that leaked through. Likely candidates:
- `RunRequest.Subdomain` assumes single shared domain — VK Cloud might use a different scheme.
- `HealthStatus` is binary — some providers expose richer states.

---

## Final Steps After All Tasks

1. Run `make lint && make build && make test` (default mode, no `-tags yandex`). All green.
2. `make dev`, deploy a repo end-to-end through UI, verify URL works.
3. Update `REFACTOR_NOTES.md` with:
   - Per-task summary (one paragraph each).
   - Deviations from this prompt and reasoning.
   - Open follow-ups.
   - Explicit list of "never tested at runtime" code (anything Yandex-tagged).
4. Do NOT update `CLAUDE.md` — that's the user's job after review.

## What NOT to Do

- Do not introduce new dependencies without justification. Stdlib + existing go.mod first.
- Do not "modernize" unrelated code (`any` vs `interface{}`, slog vs zap, etc).
- Do not rewrite passing tests in different style.
- Do not remove existing comments unless the comment is now wrong.
- Do not add emoji or marketing language to code or commits.
- Do not commit `.keys/*.json`, `.env`, secrets. If you find committed secrets, flag in NOTES and stop.
- Do not modify the saga state machine without explicit justification.
- Do not push changes that break the docker backend smoke test.

## When Stuck

Stop and report. Especially when:
- SDK API doesn't match the documentation.
- Migration doesn't apply cleanly.
- Tests fail in unrelated places.
- A task seems to need more changes than its description suggests.
- You're tempted to add a TODO that "will be fixed later" — that's a sign the task isn't well-scoped.
