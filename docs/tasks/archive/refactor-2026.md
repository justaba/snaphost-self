# Snaphost Backend Refactor — Final Summary

Status: Archived milestone summary
Current documentation: `docs/architecture/` and `docs/operations/`

**Period:** April–May 2026
**Scope:** Tasks 0–8 (main refactor scope)
**Result:** All 8 tasks closed; Task 5b deferred to backlog
**Repository:** snaphost-backend (Go monorepo)

This document is the archived milestone record of the refactor work: what was done, why, lessons learned, and what remained at that point in time.

---

## Outcomes at a glance

**Production-relevant security improvements:**
- DNS rebinding protection against cloud metadata exfiltration (Task 4).
- Vulnerable images cannot accumulate in the registry (Task 8).
- Image references validated against registry prefix allow-list and deploy state (Task 2).
- Trivy `--insecure` correctly conditional on registry type — no MITM exposure in production (Task 7).

**Architectural improvements:**
- Provider-agnostic backend abstraction preserved and reinforced across all changes.
- Error classification infrastructure (transient/permanent) ready for retry logic (Task 5a).
- Symmetric event publishing: pipeline returns metadata, worker decides finalization (Task 5a).
- Subdomain collisions eliminated via full UUID space (Task 6).
- BuildKit client/daemon versions aligned (v0.29.0 across both, upgraded during Task 8).

**Hidden bugs discovered and fixed:**
- `chk_deploys_status` constraint violation that 500-spammed user-billing for weeks (Task 4.2.5).
- Vestigial `ReportBuilt` HTTP callback that never functioned but silently failed every deploy (Task 4.2.5).
- Two parallel state machines (`deploys.status` and `deploys.current_step`) silently conflated, now documented (Task 4.2.5).

**Operational visibility:**
- Structured logging with `transient`/`permanent` boolean fields on pipeline errors.
- `image deleted from registry` log on cleanup events.
- Local registry auto-detection logged at startup.
- Saga state transitions logged with deploy_id context.

---

## Task-by-task summary

### Task 0 — Yandex backend behind build tag

**Problem:** A force-pushed pseudo-version of Yandex Go SDK broke compilation entirely. Multiple files in `runner-svc/internal/backend/yandex/` could not compile, blocking all Docker work.

**Solution:** Isolated all Yandex code behind `//go:build yandex` tag. Default `go build ./...` succeeds; `-tags yandex` shows the existing baseline errors that pre-date this refactor.

**Files:** `runner-svc/internal/backend/yandex/{yandex,iam,gateway_spec}.go`, `runner-svc/cmd/api/{yandex_enabled,yandex_stub}.go`.

**Lesson:** Compile-time isolation via build tags is a clean way to quarantine broken integrations without deleting the code or polluting the default build.

---

### Task 1 — Shared yandexauth package

**Problem:** Builder-svc had manual JWT/RSA signing + HTTP token exchange duplicated, instead of using the Yandex SDK. ~100 lines of fragile crypto code.

**Solution:** Created `shared/yandexauth/` using `yandex-cloud/go-sdk` v0.31.0 with two exports: `NewSDK(ctx, keyPath)` and `IAMToken(ctx, sdk)`. Migrated builder-svc; removed manual code.

**Files:** `shared/yandexauth/yandexauth.go`, `builder-svc/internal/build/yandex_iam.go`.

**Lesson:** Choose v0.31 over v2 SDK based on convenience wrappers (`sdk.Serverless().Containers()`). The v2 removes these and requires more boilerplate. Re-evaluate when v0 deprecates or workload identity is needed.

---

### Task 2 — Image reference validation in runner-svc

**Problem:** Runner-svc accepted any image_ref from saga. A malicious or buggy upstream could request deploy of an arbitrary image.

**Solution:** Provider-agnostic validation:
- Prefix allow-list via `AllowedRegistryPrefixes` env var.
- Tag must equal `deploy_id` (parsed via `strings.LastIndex` to handle registry hosts with colons).
- Strict mode (production) cross-checks against user-billing's `/internal/deploys/:id` endpoint — user_id matches, status is `building`, image_ref in saga matches request.

**Files:** `runner-svc/internal/runner/service.go`, `user-billing/internal/deploy/handlers.go`, `user-billing/routes/routes.go`.

**Architectural invariant established:** validation is provider-agnostic and config-driven. No hardcoded `cr.yandex` or `host.docker.internal`.

**Lesson (learned later in Task 4.2.5):** My initial spec encoded `"built"` as a deployable status, which doesn't exist in the DB. Verify code invariants against actual DB migrations before writing code, not after smoke tests fail.

---

### Task 3 — Canonical `isWithin` path containment

**Problem:** `clone/path.go::isWithin` had a custom string-prefix implementation that failed on symlinks and trailing slashes, allowing potential path traversal.

**Solution:** Canonical implementation using `filepath.Abs` + `filepath.Rel`. 15 table-driven tests covering edge cases including symlinks, `..` traversal, and case sensitivity.

**Files:** `builder-svc/internal/clone/path.go`, `path_test.go`.

**Lesson:** When the stdlib provides primitives (`filepath.Rel`), use them. Hand-rolled string operations on paths are bug magnets.

---

### Task 4 — DNS rebinding protection (the big one)

**Problem:** Git clone via go-git resolved repository hostname once, then connected. If an attacker controlled DNS for the hostname, they could resolve to `169.254.169.254` (cloud metadata service) on the second resolution and exfiltrate IAM credentials.

**Solution (4 defense layers):**

1. **IP filter** (`ErrIPFiltered`, `ErrResolverFailure`): rejects private/metadata IPs including cloud-specific addresses (`169.254.169.254`, `fd00:ec2::254`) and IPv4-mapped IPv6 (`::ffff:169.254.169.254`).
2. **Pinned dialer**: validates URL once, then passes a custom `Dialer` to go-git that connects to the validated IP only, ignoring subsequent DNS.
3. **TLS hostname verification**: `tls.Config.ServerName` set to original hostname; cert validation uses the original name despite IP-pinned connection.
4. **Syntactic-only API validation**: API enqueues without DNS lookup; worker does the security-critical DNS validation just before connection. Closes a TOCTOU window where DNS could change between enqueue and clone.

**Sub-step 4.2.5 — Bug discovery:** During smoke test, noticed `user-billing` logs showed `chk_deploys_status` constraint violation on every deploy for weeks. Root cause:
- `deploys.status` column allows 8 values (no `'built'`).
- `deploys.current_step` column allows 5 values (includes `'built'`).
- Both columns silently conflated in code; my refactor spec compounded the confusion.
- Builder-svc's `ReportBuilt` HTTP callback shipped `status="built"` payload that user-billing's handler silently discarded `image_ref`+`commit_sha` fields and tried to UPDATE with invalid status → SQL 23514.
- Deploys still completed because saga used Redis BuildEvent (not HTTP) to learn image_ref.

**Resolution:** Deleted `ReportBuilt` callback entirely (vestigial), fixed `deployableStatuses["building"]` (not `"built"`), documented the two parallel state machines in [`implementation-log.md`](implementation-log.md).

**Files:** `builder-svc/internal/clone/url_validator.go`, `cloner.go`, `path.go`, `workdir.go`, `runner-svc/internal/runner/service.go`, multiple test files.

**Lessons:**
- **Reading logs is a security tool.** This 500-spam ran for weeks; finding it required active log review during smoke test, not just "deploy succeeded".
- **State machine documentation is invariant documentation.** Two columns with overlapping values demand explicit docs. Code comments aren't enough — REFACTOR_NOTES section drove the point home.
- **Smoke test ≠ logs clean.** A deploy completing successfully says nothing about underlying health.

---

### Task 5a — Error classification + symmetric event publishing

**Problem (uncovered in investigation 5.0):**
- Worker ack'd every error from `runner.Run`, including transient ones (network blips, Trivy DB downloads).
- Pipeline always published BuildFailed on any error → saga compensated → refund → user must redeploy.
- No mechanism to distinguish "blip, try again" from "your input is bad".

**Investigation 5.0 also surfaced:**
- Redis Streams retry doesn't exist in current code (no XCLAIM reclaim loop).
- `pipeline.Run` publishes BuildFailed inside its function — coupling that prevents retry.
- Saga's 15-minute `BuildTimeout` is the implicit retry budget.

**Solution (Task 5a — architectural prep for future retry):**
- `pipeline/errors.go`: `ErrTransient`, `ErrPermanent` sentinels with `Transient(err)`, `Permanent(err)` wrappers; `IsTransient`/`IsPermanent` for callers.
- Typed sentinels at source: `clone.ErrResolverFailure`, `clone.ErrIPFiltered`, `clone.ErrHostNotAllowed`, `clone.ErrInvalidURL`, `ai.ErrAIRefused`, `ai.ErrAIUnavailable`.
- `pipeline/classify.go`: four helpers (`classifyValidationError`, `classifyGitError`, `classifyAIError`, `classifyBuildKitError`) — one per error source.
- All 17 error returns in `pipeline/runner.go::executePipeline` wrapped per classification.
- **Symmetric publishing decoupling:** `Run(ctx, Job)` signature changed `error` → `(*Result, error)`. Worker calls `FinalizeAsSucceeded(result)` on success or `FinalizeAsFailed(cause)` on error. Pipeline returns metadata only; publishing moves to worker.
- Structured worker logs include `transient`/`permanent` boolean fields.

**Task 5b deferred to backlog.** Activation of transient retry via Redis XCLAIM reclaim loop requires additional design work (max-retry budget coordinated with saga's BuildTimeout, dead-letter handling). Current behavior remains identical to pre-Task-5a — all errors finalize immediately. The infrastructure is ready when business case justifies the work.

**Files:** `builder-svc/internal/pipeline/{errors,classify,runner}.go`, `clone/url_validator.go`, `ai/client.go`, `cmd/worker/main.go`, multiple test files.

**Lessons:**
- **Investigation phase finds the real scope.** Task 5 was originally one task; investigation 5.0 revealed it had to be 5a + 5b. Without that phase, we'd have shipped retry infrastructure that didn't work at runtime.
- **Symmetry is invariant.** Splitting fail-publishing from success-publishing across layers was a code smell I almost shipped. Catching it before merge saved future debugging.
- **Production data > theoretical justification.** Task 5b waited for observability (now in place via structured logs). After a few weeks of production data, we'll know which transients are common and whether 5b is worth the engineering.

---

### Task 6 — Subdomain collision fix

**Problem:** `generateSubdomain` used 8 hex characters (32 bits entropy). Birthday paradox → 50% collision probability around 65k deploys. DB had `UNIQUE` constraint as defense, but failure UX was "deploy failed for unclear reason."

**Solution:** Full UUID (32 hex chars, dashes stripped). 122 bits entropy → collision space 2^122. Subdomain format `proj-<32-hex>` (~37 chars), well within RFC 1035's 63-char subdomain limit.

**Files:** `runner-svc/internal/runner/service.go`, `subdomain_test.go`.

**Lesson:** When choosing between "shorter ID with retry-on-collision" vs "use all available entropy", the latter is simpler and avoids edge cases.

---

### Task 7 — Trivy `--insecure` conditional flag

**Problem:** Trivy was always called with `--insecure`, which is correct for local dev (plaintext registry on `host.docker.internal:5000`) but dangerous in production (disables TLS cert verification → MITM exposure).

**Solution:**
- `cfg.RegistryInsecure bool` env-driven config.
- Auto-detect: enable for known local hosts (`localhost`, `127.0.0.1`, `host.docker.internal`, `registry:`).
- Explicit env override wins over auto-detect.
- Warn at startup if `REGISTRY_INSECURE=true` paired with a non-local registry.

**Files:** `builder-svc/config/config.go`, `internal/scan/trivy.go`, `cmd/worker/main.go`, `infra/.env.example`.

**Lesson:** Default to secure (`false`). Auto-detect for dev ergonomics. Always allow explicit override.

---

### Task 8 — Vulnerable image cleanup (the long one)

**Problem:** Trivy scanned images **after** push to the registry. If scan found CRITICAL vulnerabilities, pipeline returned an error but the vulnerable image remained in the registry, accumulating disk usage and theoretically pullable.

**The pivot:**

Initially chose **Option A** (scan before push via local OCI tarball, second push only on scan success). After **5 iterations** of failures rooted in BuildKit client/daemon version mismatch (v0.13.2 client + v0.29.0 daemon), each "fix" exposing a new failure mode:

1. `Output` filesync → malformed tarball.
2. `OutputDir` + self-tar → `diffcopy method not supported by client` (gRPC mismatch).
3. BuildKit client upgrade to v0.29.0 → `ingest/` directory contaminating OCI layout.
4. `ingest/` exclusion in self-tar → same `manifest.json not found` error (root cause unclear).
5. Decided to pivot rather than continue Option A debugging.

**Option B (final solution):** Scan after push, with explicit DELETE from registry on CRITICAL scan failure.
- `internal/registry/client.go` interface for provider abstraction.
- `internal/registry/docker_v2.go` implementation using Docker Registry v2 HTTP API: GET manifest (extract `Docker-Content-Digest` header) → DELETE by digest. Idempotent (404 → success).
- `pipeline/runner.go` calls `Registry.DeleteImage` when `errors.Is(err, ErrCriticalVulnerability)` AND `ScanFailOnCritical=true`.
- `infra/docker-compose.yml` registry service gets `REGISTRY_STORAGE_DELETE_ENABLED=true`.
- BuildKit client v0.29.0 upgrade **retained** — aligned with daemon, eliminates the original cause of Option A's problems.
- Yandex CR delete is documented future work behind `//go:build yandex` (Task 0 isolation pattern).

**Verification (negative test):**
- Deployed `alpine:3.10 + curl` (known-vulnerable base image).
- Trivy found 1 CRITICAL.
- Pipeline returned `permanent: scan: critical vulnerabilities found (CRITICAL=1, HIGH=0)`.
- DockerV2Client logged `image deleted from registry, digest=sha256:..., status=202`.
- Saga compensated, refund issued.

**Security window argument:** Option B leaves a ~30-60 second window where vulnerable image exists in registry during scan. Acceptable because:
- Task 2 image_ref validation in runner-svc blocks deploy of images not in valid `building` state.
- Image tags are unguessable (`proj-<hash>:<full-uuid>`).
- Registry on private Docker network; not externally exposed.

**Files:** `builder-svc/internal/registry/{client,docker_v2}.go`, `internal/pipeline/runner.go`, `internal/build/buildkit.go` (Option A rollback), `internal/scan/trivy.go` (Option A rollback), `cmd/worker/main.go`, `infra/docker-compose.yml`.

**Lessons (the painful ones):**
- **Sunk cost fallacy is real.** After 4 iterations of Option A, switching to Option B felt like abandoning work. It wasn't — it was the correct decision.
- **Version mismatches are landmines.** 16-minor-version gap (v0.13.2 client vs v0.29.0 daemon) is a structural fragility, not a "we'll fix it later" item. Should have been caught and fixed in Task 0 baseline.
- **Pragmatic > clean.** Option A was theoretically cleaner (zero-exposure window). Option B was implementable in one iteration because HTTP DELETE is a commodity operation. In hindsight, Option B should have been the default choice; security defense-in-depth from Task 2 made the window concern manageable.
- **Smoke tests need negative paths.** Happy path success says nothing about whether your security control works. The Task 8 negative test (`alpine:3.10 + SCAN_FAIL_ON_CRITICAL=true`) is the entire proof of work.

---

## Process improvements that became permanent

These emerged during the refactor and should continue:

### 1. Investigation phase mandatory for state-machine or error-handling tasks

Examples:
- Task 4.0 investigated go-git API to choose pinning mechanism.
- Task 5.0 investigated pipeline error sources, Redis Streams semantics, saga idempotency.
- Task 8.0 investigated BuildKit OCI export feasibility.

**Rule:** No code until investigation report is delivered and reviewed. If the report changes scope (as Task 5 did), the original spec is revised before code starts.

### 2. Acceptance criteria includes log review

Every sub-step's smoke test must include `grep` of logs from **all** services involved in the flow, not just the changed service.

The `chk_deploys_status` bug (Task 4.2.5) was undetected for weeks because nobody read user-billing logs during builder-svc smoke tests. Now this is standard:

```bash
date  # record
# deploy via UI
docker compose logs builder-worker user-billing runner-api --since=5m | grep -iE "error|fail|warn"
```

### 3. Verify invariants against migrations before writing code

State machine semantics belong to whoever owns the migrations. Don't trust prompt-driven specs that encode invariants without checking actual SQL constraints.

### 4. Behavior-preserving refactors verified by smoke test

Task 5a's symmetric publishing decoupling was a behavior-preserving refactor. Smoke test (happy + negative) confirmed identical UX before merging. Same pattern applied to Task 8 Option B rollback of Option A residue.

### 5. Use git commits as refactor checkpoints

After each task closed, `git commit -m "task N: <summary>"`. This produced 8 atomic commits forming a coherent narrative for any future bisect or rollback.

### 6. Standalone agent prompts include full context

Several tasks switched agents mid-refactor (limits, lost sessions). Each new agent received a standalone prompt with:
- Repo structure.
- Architectural invariants.
- Prior task summaries via [`implementation-log.md`](implementation-log.md).
- Specific change spec.
- Acceptance criteria.
- "What NOT to do."

This avoided knowledge loss and reduced agent ramp-up to ~10 minutes.

---

## What's still in the backlog

### Task 5b — Transient retry activation

**Status:** Deferred.

**What it would do:** Activate transient retry via Redis XCLAIM reclaim loop. Worker watches for transient errors, doesn't ack, leaves the message in PEL. A separate reclaim goroutine periodically XPENDING + XCLAIM messages idle longer than threshold, redelivers them. Retry budget coordinated with saga's 15-minute BuildTimeout (e.g. max 5 attempts at 2-minute intervals).

**Why deferred:** Need production data on transient frequency. Currently observed transients (from logs we now have):
- Trivy DB download from mirror.gcr.io (~1/week empirically).
- Registry race conditions after stack rebuild (occasional in dev).

If these stay rare, Task 5b is low-priority. If frequency grows, the infrastructure from Task 5a is already in place — only the worker loop changes.

**Estimated effort:** 1-2 days with another investigation phase (XPENDING/XCLAIM API details, dead-letter handling, integration testing).

### Yandex CR delete implementation

**Status:** Documented future work.

**What it would do:** Add `internal/registry/yandex.go` behind `//go:build yandex` tag. Uses `shared/yandexauth/` + Yandex SDK `ContainerRegistry().Image().Delete()`. Includes List-by-tag → Delete-by-ID two-step (Yandex SDK only deletes by internal ImageId).

**Why deferred:** Yandex backend is itself behind build tag (Task 0). Whoever fixes the Yandex SDK pseudo-version issue will likely add this as part of that work.

### BuildKit version pinning strategy

**Status:** Operational note.

**What it would do:** Establish an explicit policy for keeping BuildKit client (`go.mod`) and daemon (`docker-compose.yml`) versions in sync. Currently both v0.29.0; future upgrades should bump both atomically with smoke tests.

**Why important:** Task 8's 5-iteration debugging was caused entirely by version drift. Operational ritual prevents recurrence.

### Runtime failure refund

**Status:** Edge case observed; not a blocker.

**What it does (or doesn't):** Currently saga compensates (refunds) only on failures during build/scan phases. A failure during runtime startup (e.g. health check timeout) does not trigger refund — the deploy is marked failed, but user paid for a "successful" build.

**Why edge case:** Build succeeded, image is in registry, scan passed. The user got a real build product even if their app doesn't run. Reasonable economic argument for not refunding.

**Counter-argument:** From user perspective, the deploy failed. They couldn't reach the URL. Pay-for-success seems fair.

**Action:** Surface to product team for decision. Code change is small if decision is "refund on any failure": extend saga's compensate path to include runtime failure detection.

### implementation log → architecture documentation migration

**Status:** Considered but not done.

**What:** The implementation log grew to a substantial document during refactor. Current maintainers use `docs/architecture/`, which is framed as "how the system works" rather than "what we changed."

**Action:** Optional. The notes file remains useful as historical record. A separate clean architecture doc can be written when someone has the bandwidth.

---

## Architectural invariants preserved across all 8 tasks

These are non-negotiable design properties that survived every refactor decision:

1. **Provider-agnostic backend abstraction.** Docker, Yandex, VK Cloud all routed through `runner-svc/internal/backend.Backend` interface. No provider-specific code outside `backend/<provider>/`.

2. **Compile-time isolation of broken providers.** `//go:build yandex` keeps default builds green even when Yandex SDK is in a force-pushed pseudo-version state.

3. **Saga as state machine.** State transitions happen via `deploys.status` (user-facing) and `deploys.current_step` (saga-internal). These are explicit and documented.

4. **Redis pub/sub for build events.** Saga learns build outcomes via Redis BuildEvent, not HTTP callbacks. Decoupled and load-bearing.

5. **Webhook secret for internal endpoints.** All `/internal/*` HTTP calls between services authenticated via shared secret.

6. **Error classification (post-Task-5a).** All pipeline errors return either `pipeline.Transient` or `pipeline.Permanent` wrapped. Callers route on classification.

7. **Defense in depth.** Security controls layered:
   - URL validation (syntactic + IP filter).
   - Pinned dialer (prevents DNS rebinding).
   - TLS hostname verification.
   - Image reference validation (Task 2).
   - Vulnerability scanning with gate (Tasks 7, 8).
   - Registry cleanup on scan failure (Task 8).

---

## File-level summary of major changes

### Created
- `shared/yandexauth/` — Yandex SDK auth helpers.
- `runner-svc/internal/runner/subdomain_test.go` — Task 6 tests.
- `builder-svc/internal/pipeline/errors.go` — error type infrastructure.
- `builder-svc/internal/pipeline/classify.go` — classification helpers.
- `builder-svc/internal/pipeline/finalize_test.go` — symmetric finalize tests.
- `builder-svc/internal/registry/client.go` — Registry interface.
- `builder-svc/internal/registry/docker_v2.go` — Docker Registry v2 cleanup client.
- `builder-svc/internal/registry/docker_v2_test.go` — registry tests.
- [`implementation-log.md`](implementation-log.md) — running history (kept as archive).

### Significantly modified
- `runner-svc/internal/backend/yandex/*.go` — build tag isolation.
- `runner-svc/internal/runner/service.go` — image validation (Task 2) + subdomain (Task 6).
- `user-billing/internal/deploy/*.go` — `/internal/deploys/:id` endpoint.
- `user-billing/db/migrations/0003_deploys.up.sql` — referenced (not modified) extensively.
- `builder-svc/internal/clone/url_validator.go` — typed sentinels.
- `builder-svc/internal/clone/cloner.go` — pinned dialer.
- `builder-svc/internal/clone/path.go` — canonical isWithin.
- `builder-svc/internal/build/buildkit.go` — BuildKit upgrade + Option A residue removal.
- `builder-svc/internal/scan/trivy.go` — conditional `--insecure`.
- `builder-svc/internal/pipeline/runner.go` — three-phase build flow (later flattened) + classification.
- `builder-svc/cmd/worker/main.go` — symmetric finalize + registry client.
- `builder-svc/config/config.go` — RegistryInsecure auto-detect.
- `builder-svc/go.mod` — BuildKit v0.13.2 → v0.29.0.
- `infra/docker-compose.yml` — REGISTRY_STORAGE_DELETE_ENABLED.
- `infra/.env.example` — REGISTRY_INSECURE documentation.

### Removed
- `builder-svc/internal/build/buildkit_test.go` — Option A test file (no longer needed).
- Manual JWT/RSA code in builder-svc (replaced by yandexauth).
- `ReportBuilt` callback in builder-svc + httpStatusReporter (vestigial).
- `OutputModeLocalTar`, `tarOCIDirectory`, related code (Option A rollback).

---

## Recommended next steps

In rough priority order:

### Short term (1-2 weeks)

1. **Sit with this state.** Don't start new refactor work immediately. Operate the system, observe logs, see what breaks in real usage. The structured logs added in Task 5a give you better visibility than before — use them.

2. **Decide on the BuildKit upgrade ritual.** Document somewhere (CONTRIBUTING.md, ops runbook) that BuildKit client `go.mod` and daemon `docker-compose.yml` must be bumped together with smoke test.

3. **Surface runtime-failure-refund question to product.** Edge case but easy to clarify with a single conversation.

### Medium term (1-3 months)

4. **Watch transient error frequency.** If Trivy DB or registry race conditions become noticeable, prioritize Task 5b.

5. **Yandex provider work.** When someone resolves the SDK pseudo-version issue, integrate Yandex CR delete as part of that work.

6. **Production deployment checklist.** Several Task 7 and Task 8 settings (`SCAN_FAIL_ON_CRITICAL`, `REGISTRY_INSECURE`, `REGISTRY_STORAGE_DELETE_ENABLED`) must be correct in production. A pre-deploy checklist would prevent mistakes.

### Long term (3+ months)

7. **Architecture doc separate from REFACTOR_NOTES.** When you next have bandwidth, write `docs/architecture.md` as a clean "how the system works" reference. Refactor notes remain as historical record.

8. **Test coverage audit.** This refactor added many tests but also revealed that some integration paths (pipeline-level retry behavior) weren't testable without interface refactoring. Decide if that interface work is worth doing.

---

## Reusable patterns from this refactor

These patterns generalized well and could apply to other projects:

### Pattern 1 — Build tag isolation for broken modules

Use `//go:build <tag>` to quarantine non-compiling code (e.g. third-party SDK in bad state). Default build green; explicit `-tags <tag>` exposes the work-in-progress.

### Pattern 2 — Pre-task investigation phase

For any task touching state machines, error handling, or external integrations, the first deliverable is an investigation report, not code. Spec gets adjusted based on findings.

### Pattern 3 — Two-step provider abstraction

When supporting multiple cloud providers:
- Interface in `internal/<feature>/client.go`.
- Per-provider implementations: `<provider>.go` (or `<provider>.go` + `//go:build <provider>` if SDK is heavy).
- Factory in `cmd/<service>/main.go` selects based on config.

This was applied to runner backend (Task 0+) and registry cleanup (Task 8).

### Pattern 4 — Symmetric event publishing

Don't publish events inside business logic — return data from business logic, let the worker decide what events to publish. Symmetric for success and failure paths. Enables retry logic without restructuring business code.

### Pattern 5 — Defense-in-depth with explicit layer documentation

Multiple layers of security control, each documented in [`implementation-log.md`](implementation-log.md) with explicit "this layer defends against X." Makes future audits possible.

### Pattern 6 — Refactor notes as living document

A single markdown file tracking what changed, why, file references, smoke test results, and lessons learned per task. Keeps refactor coherent across multiple agents and time.

---

## Closing notes

**Time invested:** Multi-week refactor, but spread out — actual focused work probably 5-7 working days equivalent across agents and review cycles.

**What worked best:**
- Investigation phases before code (saved Task 5 from a fundamentally broken design).
- Standalone agent prompts (recoverable from context loss).
- Active log review during smoke tests (found the silent state machine bug).
- Willingness to pivot mid-task (Task 8 Option A → Option B after 5 iterations).

**What could have worked better:**
- BuildKit version alignment should have been part of Task 0 baseline.
- Task 8 Option A vs B should have been a coin flip given equivalent security guarantees — chose A on aesthetics, paid the iteration cost.
- More integration testing (vs unit testing) would have caught Task 8 issues earlier. Pipeline-level tests need interface refactoring of `Cloner`/`Builder`/`Scanner` which was scoped out.

**What I'm most proud of:**

The unintended Task 4.2.5 discovery — finding a production bug that 500-spammed user-billing for weeks. That bug would not have surfaced without active log reading. The discipline of "smoke test means read logs of all involved services" originated from this refactor and is now permanent practice.

**Where the system is now:**

Production-ready security posture for DNS rebinding, image vulnerability handling, image reference forgery. Provider-agnostic architecture preserved. Observability infrastructure in place (structured logs, classification fields). Future retry work has clear implementation path. Operational risks documented.

The refactor closed all 8 originally-scoped tasks plus 1 unscheduled hidden bug. Net positive.

---

**End of summary.**
