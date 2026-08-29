# Task 11 — Reproducible production deployment

**Status:** In progress — production is live and serving; operations work
(backup schedule, monitoring, runbook) landed 2026-08-04; a production rollback
rehearsal and the approval-gate decision remain
**Created:** 2026-07-03
**Updated:** 2026-08-04

## Where this actually stands (2026-08-04)

Production exists and serves traffic. Earlier revisions of this document say it
does not; that was true when they were written and is no longer. Verified
externally on 2026-08-04:

| Check | Result |
| --- | --- |
| `production-deploy.yml` runs | 3 total, most recent successful 2026-08-03 11:18Z |
| `https://api.snaphost.ru/health` | `200` |
| `https://api.snaphost.ru/api/v1/deploys` unauthenticated | `401` |
| `https://snaphost.ru/`, `https://www.snaphost.ru/` | `200`, `301` |
| Unknown deploy host on `snaphost.pw` | `404` through the router |
| PostgreSQL `5432`, Redis `6379`, BuildKit `1234` on the host | closed |
| TLS on both domains | Let's Encrypt, ~89 days remaining |

That closes the "PostgreSQL, Redis, and BuildKit are not publicly exposed"
criterion by observation rather than by configuration review, and it closes the
Phase 2 items about durable endpoint configuration and live service-to-service
connectivity — a production deploy cannot succeed and serve without them.

The `404` from the deploy suffix is worth stating separately: it exercises
wildcard DNS, the Yandex API Gateway, `router-svc`, and its route lookup back
into `user-billing` in one request, so the whole runtime path is confirmed
alive, not just the control plane.

### Landed 2026-08-04

- **Scheduled backups.** [infra/backup.sh](../../../infra/backup.sh) plus a
  systemd timer: daily dump, archive verification before publication, `age`
  encryption, off-host S3 copy with confirmation, and a retention policy that
  never prunes a backup the deployment state still references. Covered by
  [infra/tests/backup_test.sh](../../../infra/tests/backup_test.sh) — 31
  scenarios, all passing. See [backups.md](../../operations/backups.md).
- **Monitoring.** [uptime.yml](../../../.github/workflows/uptime.yml) probes the
  six public invariants above every 10 minutes and opens/closes one GitHub
  issue per incident, plus a backup heartbeat for the dead-man's-switch case.
  Its limits are documented rather than glossed over in
  [monitoring.md](../../operations/monitoring.md): GitHub schedules are
  best-effort, nothing inside the VDS is watched, and no metrics are collected.
- **Rollback coverage and rehearsal procedure.** Ten new rollback scenarios in
  [infra/tests/deploy_test.sh](../../../infra/tests/deploy_test.sh), plus the
  stdin-consumption and service-account-identity cases added since — 48 passing
  there and 31 in the backup suite — and a step-by-step production rehearsal in
  [rollback.md](../../operations/rollback.md).
- **CI actually runs the deployment scripts.** Both shell suites, `shellcheck`,
  and `systemd-analyze verify` now run in `pipeline.yml` and gate image
  publication. They were previously run by hand in a container, which meant a
  regression in `deploy.sh` could reach production unnoticed.

### Still open

1. **Production rollback rehearsal — done 2026-08-03**, recorded in
   [rollback.md](../../operations/rollback.md). The guard, dry run, ordered
   rollout, smoke, and atomic state swap all behaved against real production
   state in 6.5 seconds with no container recreated. **What remains unproven is
   an image-changing rollback**: `current.env` and `previous.env` held the same
   SHA, so Compose had nothing to swap. That needs two different SHAs on
   production and is a real deployment, not a rehearsal.

   The rehearsal surfaced three host/documentation mismatches, all now fixed in
   the docs:
   - `/opt/snaphost/current` did not exist, although
     [deploy-remote.sh](../../../.github/scripts/deploy-remote.sh) creates it
     after a successful deployment. **Root-caused and fixed 2026-08-04**: the
     remote half of a deployment runs as `ssh host bash -s <<EOF`, and
     `docker compose exec -T` / `compose run` inside `deploy.sh` forwarded
     stdin to the container, draining the pipe bash was reading its own source
     from. The `mv` that creates the symlink was discarded before bash parsed
     it, and bash exited `0`, so CD reported success. Every `compose exec/run`
     now redirects `</dev/null`, `deploy-remote.sh` redirects as a second layer
     and asserts the symlink exists, and a regression test pipes a script into
     `bash -s` to prove the trailing line runs. The symlink was restored on the
     host. Full write-up in [rollback.md](../../operations/rollback.md).
   - `deploy.sh`'s built-in defaults point at
     `/opt/snaphost/infra/docker-compose.prod.yml`, a bootstrap leftover from
     2026-08-02 rather than the deployed release. Running it without the CD
     environment block would act on a stale manifest.
   - the local SSH key on the operator workstation authenticates as **root**,
     not `deployer`. Running `deploy.sh` as root would leave root-owned state
     and backup files that the next CD run, which connects as `deployer`,
     cannot replace. The rehearsal used `sudo -u deployer` and ownership was
     verified afterwards.
2. **Restore drill from an encrypted, off-host backup.** The staging drill
   restored a local, unencrypted dump. The path that matters if the VDS is lost
   — decrypt with the offline key, restore from the bucket — is unproven, and
   it is the only step that confirms the private key is where someone thinks it
   is.
3. **Approval gate.** Blocked by the repository being private on the Free plan;
   this is a plan decision, not work. See the acceptance criteria below.
4. **Staging.** Released 2026-08-03, so a commit now reaches production without
   a rehearsal anywhere. Either bring it back per
   [staging-deployment.md](../../operations/staging-deployment.md) or amend the
   criterion that names it.
5. ~~**[Task 12](0012-credential-separation.md)**~~ — done 2026-08-04. Keys are
   split per environment and preflight refuses a key whose service account does
   not match the environment, verified against production. Only Lockbox
   sourcing remains open there.
6. **Supabase user-seed webhook on the production project** — the migrations
   and RLS fixes landed (`0d62c59c`, `6c128fa8`, `d2e725e2`); confirming the
   dashboard-side configuration needs one test signup (see below).
7. **In-host observability.** Every service exposes `/metrics` and nothing
   scrapes it. Out of scope for this task; it belongs with
   [Task 13](0013-observability-and-user-feedback.md).

## Problem

SnapHost's Yandex runtime was proven with temporary VDS and manual smoke
infrastructure, but there is no durable deployment definition that can recreate
the control plane from versioned configuration. Local Compose builds development
images. CI publishes immutable images but deliberately does not deploy them.

## Target architecture

- A durable VDS runs API gateway, user-billing, builder API/worker, runner
  API/watchdog, AI orchestrator, PostgreSQL, Redis, and BuildKit.
- Yandex Cloud runs user containers, `router-svc`, API Gateway, DNS,
  certificate, Lockbox, and Container Registry.
- Production consumes GHCR images pinned by Git SHA. Nothing is built on the
  production host.
- Terraform owns Yandex infrastructure. A production Compose manifest owns VDS
  services. `infra/docker-compose.yml` remains development-only.

## Work plan

### Phase 1 — Contract and production manifest

1. [x] Document service placement, public endpoints, internal connections,
   persistent data, and the Compose/Terraform ownership boundary.
2. [x] Add `infra/docker-compose.prod.yml` with immutable `image:` references.
3. [x] Add `infra/.env.production.example` with names and safe examples only.
4. [x] Add executable health checks where current images support them, plus
   restart policies, volumes, and resource limits. Other HTTP probes remain a
   documented follow-up.
5. [x] Keep PostgreSQL, Redis, and BuildKit internal without host ports.

Phase 1 validation on 2026-07-03:

- Compose config validation passed and returned the ten expected VDS services;
- inspection found no `build:`, `latest`, local Registry, Traefik, Docker
  socket, or Terraform bootstrap key;
- infrastructure services have no host port mappings;
- builder and runner key mounts match their intended service boundaries;
- configured healthcheck commands match known image contents and Compose config
  passes; runtime health transitions were not tested by starting containers;
- local Markdown links and `git diff --check` were checked.

Task 11 remains active. HTTP probes for distroless services and runner require
a separate minimal runtime-image or native probe change.

`SCAN_FAIL_ON_CRITICAL=true` stops the pipeline and prevents runner startup for
an image with CRITICAL vulnerabilities. Cleanup is currently implemented only
for local Docker Registry v2, so the rejected image may remain in Yandex
Container Registry. Yandex registry deletion remains a separate
[backlog item](../backlog.md) and must not be treated as implemented production
cleanup.

### Phase 2 — Secrets and network hardening

1. [x] Mount the builder key only into `builder-worker`.
2. [x] Mount the runner key only into `runner-api` and `runner-watchdog`.
3. [x] Keep Terraform bootstrap credentials outside runtime services.
4. [x] Keep router authentication in metadata/Lockbox mode.
5. [ ] Replace the temporary router billing URL with a durable endpoint. The
   exact secret-protected API gateway ingress is implemented; durable TLS URL
   configuration and live connectivity remain unverified.
6. [ ] Verify all required service-to-service connections at runtime.

Phase 2 implementation validation on 2026-07-03:

- API gateway config requires distinct `SUPABASE_WEBHOOK_SECRET` and
  `WEBHOOK_SECRET` values and passes the latter to its internal billing client;
- only `GET /internal/routes` is registered before user JWT/Casbin middleware;
- focused tests cover missing/wrong secrets, no user authorization, query and
  upstream response preservation, unavailable billing, wrong method, and an
  adjacent internal path;
- API gateway `go test ./...`, `go vet ./...`, and
  `go build -buildvcs=false ./...` passed;
- production Compose and Terraform validation passed; secret mounts, networks,
  host ports, and Lockbox references were audited.

Remaining Phase 2 blockers are durable TLS endpoint configuration, firewall
and rate-limit policy, and a live Yandex router-to-VDS connectivity test.

### Phase 3 — Deployment and rollback

1. [x] Add a deployment script accepting an immutable Git SHA.
2. [x] Validate env and secret files before changing services.
3. [x] Pull target and recorded rollback images before rollout.
4. [x] Back up PostgreSQL and run profile-only migration jobs as a controlled
   step using dedicated migrate binaries.
5. [x] Start services in dependency order with bounded health/HTTP/stability
   checks and publish API gateway last.
6. [x] Run narrow smoke checks and implement guarded image rollback. Rollback
   after migration start requires explicit backward-compatibility confirmation;
   database restore is never automatic.

Phase 3 implementation validation on 2026-07-03:

- user-billing and ai-orchestrator migrate binaries passed `go test ./...`,
  `go vet ./...`, and `go build -buildvcs=false ./...`;
- default Compose still contains ten services and the `migration` profile adds
  two one-shot jobs;
- Compose config validation passed;
- `bash -n` passed for `deploy.sh` and its test runner in a local Bash 5.3
  container; all 27 fake-command scenarios passed without production Docker,
  network, GHCR, or secret access; no deployment command was executed.

Phase 3 review hardening on 2026-07-03 additionally verified strict `0400/0600`
file modes (including rejection of `0640`/`0644`), filesystem-read-only rollback
dry-run, sensitive curl-config cleanup on success/error/INT/TERM, exclusive
backup/checksum publication, retained failed-deployment state, and concurrent
locking. First deployment leaves `previous.env` absent, and checksum publication
failure removes the dump created by that same attempt.

Phase 3 is not production-proven until the shell suite runs on Linux and a
staging rollout proves backup, migrations, readiness, smoke, state transitions,
and both rollback branches.

### Phase 4 — Durable staging and CD

1. [ ] Create persistent staging with separate data, secrets, domain, and
   resource names. Repository inventory is documented; resources do not yet
   exist or have not been verified.
2. [x] Use the same manifest and deployment script contract in staging and
   production.
3. [x] Implement repository-side staging CD after the complete image matrix on
   pushes to `main`. First live staging deployment remains pending.
4. [ ] Require an approved GitHub Environment before production deployment.
   The manual workflow uses `environment: production`; required reviewers and
   secrets are external GitHub settings and remain unverified.

Phase 4.1 repository implementation on 2026-07-03 includes exact-SHA manifest
verification for all six images, strict pre-provisioned SSH host verification,
non-root remote deployment, versioned release upload, remote preflight, atomic
post-deploy `current` switch, encoded/strictly validated remote parameters,
production ancestry validation against `origin/main`, per-environment
non-cancelling concurrency, automatic staging only after `images`, and manual
production `workflow_dispatch`. A validated environment-specific Compose
project name remains constant across release SHAs, so networks and named
volumes are reused rather than recreated per release directory.

The live staging bootstrap exposed an unnecessary Cloudflare dependency. For
staging, frontend delivery now uses the verified SSH identity, a versioned Vite
artifact, an atomic VDS symlink, Caddy HTTPS, smoke validation, and symlink
rollback. Production Cloudflare/CDN delivery is explicitly deferred and
remains mandatory work before launch.

Separate staging Yandex resources, VDS, HTTPS/DNS/firewall, and GitHub staging
Environment values are now configured. The first exact-SHA deployment reached
the VDS but failed at BuildKit readiness before migrations. Ubuntu 24.04
AppArmor denied `/proc/self/exe` for the rootless container. A versioned named
profile granting only the required `userns` transition was tested live:
BuildKit became healthy and `buildctl debug workers` succeeded without disabling
the host-wide restriction. A clean workflow rerun and live
smoke/backup/migration/rollback evidence remain. Phase 4 is not complete.

The subsequent rollout passed infrastructure readiness and applied migrations,
then stopped safely because `builder-worker` could not read its `0600` key: the
image's dynamically allocated `snaphost` UID differed from the VDS deployer's
UID. The builder image and VDS contract now use the explicit non-root identity
`1000:1000`. Key permissions remain strict, and post-migration failure state and
the PostgreSQL backup were retained for manual review before another rollout.

Phase 4.2 staging infrastructure contract on 2026-07-03:

- [x] Audit the existing single Terraform root, local key generation,
  hardcoded names, DNS/certificate, registry, IAM, router, and gateway risks.
- [x] Define an environment-specific S3 backend config, staging tfvars
  template, staging key output directory, production-compatible name defaults,
  and ignored state/credential/plan artifacts without changing resource
  addresses.
- [x] Require dedicated staging and production state buckets, mutually
  inaccessible backend identities, versioning, encryption, public-access
  blocking, and an automated expected-service-account/bucket access gate before
  init, state reads, and plans, including a negative owner-mismatch check.
- [x] Document staging VDS sizing, tools, non-root ownership, firewall, paths,
  permissions, required owner inputs, safe init/validate/plan/review sequence,
  and a separate future production state migration plan.
- [x] Review a real staging bootstrap plan. On 2026-07-04 the isolated empty
  backend produced a saved plan with 21 creates, 0 changes, and 0 destroys;
  all resources target the staging folder/domain and the router remains
  disabled until its immutable image exists.
- [x] Apply the bootstrap staging resources. On 2026-07-05 the reconciled
  environment contained 21 resources, produced a zero-drift plan, and served
  the expected HTTPS dummy `404` through `probe.kinocassa.ru`.
- [x] Configure the GitHub `staging` Environment and required values.
- [x] Prove the first deployment, live smoke, backup creation, and migrations.
- [ ] Prove guarded rollback and PostgreSQL backup restore.

The first backend staging deployment completed successfully on 2026-07-05 at
SHA `73bd69a5f8753f8ea899973b892f192feadaf7e4`. All ten containers were running,
public API health succeeded, negative route authentication returned `401`,
`current.env` recorded the exact SHA, and no `in-progress.env` remained.
Frontend VDS deployment and explicit rollback proof are still open.

The next rollout exposed a readiness race: API Gateway had entered Docker's
running state, but the public smoke ran before its HTTP listener was ready.
Both local and public GET `/health` returned `200` moments later. Deployment now
requires a bounded internal HTTP probe before public Caddy/TLS smoke checks.

The corrected full staging deployment completed successfully at SHA
`2286b2da3f60329611488de23b8f060ceebb9d5b`. `current.env` records successful
migrations and smoke with no unfinished state. All ten backend containers were
still running after two hours; API HTTPS health passed. The atomic frontend
symlink points to the same SHA and its Caddy HTTPS endpoint returned `200` with
the expected security headers. First end-to-end rollout proof is complete;
guarded rollback and backup restore proof remain open.

The first frontend artifact exposed a missing build-time contract:
`VITE_API_URL` was not passed by CI, so the browser stopped before rendering.
All required Vite endpoints are now explicit repository variables, checked for
non-empty values before build, and documented in `snaphost-ui/.env.example`.

The first authenticated browser check then exposed a missing Compose boundary:
`CORS_ALLOW_ORIGINS` was not passed to API Gateway, so preflight `OPTIONS`
requests reached JWT middleware and returned `401`. The variable is now
mandatory, explicitly allowlisted per environment, passed by Compose, and
covered by a focused preflight-before-auth test.

The first UI-triggered project build exposed a registry-auth boundary defect:
the custom Yandex IAM BuildKit auth server replaced BuildKit's standard auth
flow and rejected Docker Hub token-authority requests while resolving public
base images. Yandex IAM credentials are now supplied through BuildKit's
standard provider only for the exact host derived from `REGISTRY_URL`; public
registries retain anonymous OAuth/token-authority support, and lookalike hosts
cannot receive Yandex credentials.

After registry authentication succeeded, the first Dockerfile `RUN` exposed a
second rootless-runtime boundary: Docker's default masked system paths blocked
the nested OCI executor from mounting procfs. Compose now adds the upstream
required `systempaths=unconfined` option only to `buildkitd`. The daemon remains
non-root and retains its named AppArmor profile; readiness evidence must include
an executed build step rather than only `buildctl debug workers`.

The next pipeline attempt built and pushed the project image but Trivy could
not authenticate its remote Yandex Registry pull. Scanner authentication now
reuses the builder identity to obtain a short-lived IAM token, writes it only
to a temporary mode-`0600` Docker config scoped to the exact registry host,
forces Trivy's `remote` image source, and removes the config after the scan.
Lookalike or unexpected image hosts are rejected before credentials are minted.

The first successful user-container deployment then exposed that staging's API
Gateway still used its bootstrap dummy `404` integration. On 2026-07-06 an
immutable router image from commit `d53e4da` was pushed, a deletion-protected
Lockbox secret and least-privilege reader binding were created, and the reviewed
Terraform plan applied exactly `2 add, 1 change, 0 destroy`. The central router
is active and the existing gateway now uses the `serverless_containers`
integration. The original test deployment had already reached its configured
10-minute TTL and was deleted by watchdog before router activation, so a fresh
UI-to-public-URL smoke remains required.

A fresh static frontend deploy then reached the user Serverless Container but
returned Yandex `UserCodeError` on request. The image was a generated
`nginx:alpine` runtime that hardcoded `listen 80`/`EXPOSE 80`, while Yandex
Serverless Containers require the app to listen on the injected runtime
`PORT`. The static nginx, CRA, Angular, and Vite templates now generate an
nginx runtime template that listens on `${PORT}`, set `ENV PORT=8080`, and
expose `8080`. The fix is covered by `ai-orchestrator/internal/templates`
tests; a fresh UI-to-public-URL smoke with a rebuilt user image is still
required.

The next fresh deploy still reused the old broken image because
`ai_dockerfile_cache` returned a stale template-generated Dockerfile from
before the port-contract change. Task 11.18 bumps the AI Dockerfile cache
schema version from `v1` to `v2`, making old rows unreachable, and aligns the
static nginx template `ExposePort` values with the generated `8080` runtime
port. New deploys must therefore regenerate the Dockerfile instead of reusing
the stale cache row.

Phase 4.3 preparation created separate staging/production state and logging
buckets, backend identities, a staging bootstrap identity, and separate
deletion-protected KMS keys. Backend identities were verified to access only
their own bucket. Buckets are private, versioned, and KMS-encrypted. Explicit
Object Storage access logging still returns `AccessDenied`; applying a generic
AWS `aws:SecureTransport` policy proved incompatible and was fully removed.
The fixed HTTPS Yandex endpoint is formally accepted as the TLS control by
[ADR 0005](../../decisions/0005-terraform-state-backends.md). Access logging
has an owner-approved exception for the first staging apply as of 2026-07-05.
It remains a mandatory [backlog](../backlog.md) item that must be enabled and
verified for both state buckets before any production apply.

The first staging apply was authorized and attempted on 2026-07-05. The
fail-closed backend and plan gates passed, and 17 of the original 21 resources
were confirmed in remote state. The nested staging DNS zone required parent
zone authority, so it was created with the operator profile and imported
without granting the staging bootstrap account access to the parent zone.

Inherited proxy environment variables caused the apparent CLI/backend outage;
direct requests without proxy completed normally. Yandex rejected the original
staging custom-domain attachment because its domain object was already attached
to the existing production gateway. The conflict was resolved by dedicating
`kinocassa.ru` to staging. DNS/certificate replacement and gateway replacement
were applied separately, with the second stage gated on certificate status
`ISSUED`. The reconciled environment now has 21 resources and a zero-drift
plan; `probe.kinocassa.ru` resolves through the wildcard record and returns the
expected HTTPS dummy `404`. No production state migration was performed.
Backend config, tfvars, plans, provider directory, and credentials remain
ignored and outside the committed repository.

### Phase 5 — End-to-end proof and operations

1. [x] Deploy through the normal UI/API and saga path.
2. [x] Prove BuildKit push, Trivy scan, strict validation, Yandex pull, and
   public router access in one scenario.
3. [x] Prove public delete and watchdog TTL cleanup.
4. [x] Review all service logs.
5. [x] Add database backups, restore testing, monitoring, and an operator
   runbook — implemented 2026-08-04. Scheduled backups with retention,
   encryption, and an off-host copy ([backups.md](../../operations/backups.md));
   an external probe with alerting ([monitoring.md](../../operations/monitoring.md));
   rollback and rehearsal procedures ([rollback.md](../../operations/rollback.md)).
   Two proofs remain and are listed under "Still open": a production rollback
   rehearsal and a restore drill from an encrypted off-host backup.

Phase 5 proof on 2026-07-08, on a rebuilt staging VDS (`5.42.117.227`) after the
previous host was reprovisioned and lost:

- Public JWT path: a confirmed Supabase account logged in, and `POST
  /api/v1/deploys` with the bearer JWT drove the full saga through api-gateway
  (JWT + Casbin) → user-billing → builder (clone → AI Dockerfile → BuildKit →
  Trivy → push to the staging Yandex registry) → runner (Yandex Serverless
  Container) → central router. `https://proj-<id>.kinocassa.ru` returned `200`;
  `DELETE` returned `204` and the URL then returned `404`.
- An internal path (user-billing `/api/v1/deploys` with `X-User-ID`, bypassing
  the gateway) proved the identical build→run→router→delete lifecycle
  independently.
- Guarded rollback: deploying a second SHA then `deploy.sh rollback` was
  correctly blocked without `MIGRATIONS_BACKWARD_COMPATIBLE`, and succeeded with
  it (readiness + smoke + atomic swap).
- Backup/restore: `deploy.sh` took a pre-deploy custom-format dump; it was
  checksum-verified and restored into an isolated `--network none` container with
  all eight tables present, leaving the live database untouched.

Four environment-contract bugs were found and fixed during the rebuild, all now
documented: a leftover development `SUPABASE_URL` crash-looping api-gateway on
JWKS prefetch; Caddy `403` because `/opt/snaphost` was `0750` (needs `0751`
traversal); production builder/runner keys deployed to staging causing a
registry-push `403` (scoped as [Task 12](0012-credential-separation.md)); and a
missing Supabase user-seed webhook, so a freshly registered user has no wallet
and cannot deploy until seeded (see below).

## Acceptance criteria

- A clean VDS starts from documented prerequisites, one manifest, one version,
  and externally supplied secrets. **Met** — production was brought up this way.
- Every backend image is pinned to the requested Git SHA. **Met.**
- PostgreSQL, Redis, and BuildKit are not publicly exposed. **Met** — confirmed
  against the production host on 2026-08-04, not only in configuration.
- Builder and runner keys are mounted only into intended services. **Met** in
  the manifest; the guard that would catch a *wrong-environment* key is
  [Task 12](0012-credential-separation.md).
- Missing configuration stops deployment before rollout. **Met.**
- Rollback is documented and tested. **Met** — twelve scenarios in CI, the
  staging proof, and a production rehearsal on 2026-08-03. An image-changing
  rollback on production is still unproven.
- ~~Permanent staging passes UI-to-Yandex deploy, URL, delete, and TTL
  scenarios.~~ **Cannot be met as written.** It was proven on staging on
  2026-07-08, but staging was released on 2026-08-03. The equivalent production
  proof is the Phase 5 deploy path plus the runtime `404` check in the uptime
  probe; a full UI-to-URL-to-delete cycle against production has not been run.
  Either restore staging or replace this criterion with a production one — it
  should not sit here reading as satisfied.
- Production uses the same versioned contract with an approval gate. The
  versioned contract is met; a GitHub *required-reviewers* approval gate cannot
  be enabled while the repository is private on the Free plan (the API returns
  `422`). The `production` environment restricts deployments to `main` and
  production runs are manual `workflow_dispatch` with `origin/main` ancestry
  validation; a full reviewer gate needs a public repository or a paid plan.
- Backup and restore are documented and tested. **Backup: met** — scheduled,
  verified, encrypted, off-host, with retention, covered by 31 tests.
  **Restore: partially** — the staging drill restored a local unencrypted dump;
  the encrypted off-host path is unproven.
- Monitoring exists and alerts a human. **Met at a floor** — an external probe
  every 10 minutes with GitHub-issue alerting. Nothing inside the VDS is
  watched and no metrics are collected; both limits are recorded in
  [monitoring.md](../../operations/monitoring.md).

## Remaining before production launch

- Configure the Supabase user-seed webhook (below); without it, registered users
  have no wallet. The SQL side is owned by
  [`justaba/snaphost-supabase`](https://github.com/justaba/snaphost-supabase/tree/main/supabase/migrations);
  the dashboard
  configuration needs one test signup to confirm.
- Resolve the approval-gate plan limitation (public repo or paid plan).
- Prove an image-changing rollback ([rollback.md](../../operations/rollback.md)).
- Drill a restore from an encrypted off-host backup
  ([backups.md](../../operations/backups.md)).
- Decide whether staging returns, since nothing rehearses a migration today.

### Supabase user-seed webhook

api-gateway exposes `POST /internal/webhooks/supabase`
([webhooks/supabase.go](../../../snaphost-backend/internal/gateway/webhooks/supabase.go)),
authenticated by `X-Webhook-Secret` against `SUPABASE_WEBHOOK_SECRET`. On an
`INSERT` whose `type` is `INSERT` and `table` is `profiles` or `users`, with a
`record` carrying `id` and `email`, it calls user-billing `/internal/users` to
seed the initial vibecoin balance. The endpoint itself is verified working on
staging (2026-07-08): a wrong secret returns `401`, an empty record `400`, and a
valid simulated `INSERT` returns `200 {"status":"ok"}` and creates the wallet.

What is missing is the Supabase-side configuration. Supabase auth writes new
users into `auth.users`, but Database Webhooks fire on the `public` schema, so a
direct webhook on `auth.users` is not available. Wire it as:

1. A trigger mirroring new auth users into a public table, e.g.:

   ```sql
   create table if not exists public.profiles (
     id uuid primary key references auth.users(id),
     email text
   );
   create or replace function public.handle_new_user()
   returns trigger language plpgsql security definer as $$
   begin
     insert into public.profiles (id, email) values (new.id, new.email);
     return new;
   end; $$;
   create trigger on_auth_user_created
     after insert on auth.users
     for each row execute function public.handle_new_user();
   ```

2. A Database Webhook on `public.profiles` `INSERT` → `https://api.kinocassa.ru/internal/webhooks/supabase`
   with header `X-Webhook-Secret: <SUPABASE_WEBHOOK_SECRET>` (the value from the
   environment file). The handler accepts `table` `profiles` or `users`.

Until this is configured on the cloud Supabase project, a newly registered user
has no wallet and gets `404 wallet_not_found`; the operator must seed manually
via user-billing `/internal/users`. Production and staging use different Supabase
projects and different `SUPABASE_WEBHOOK_SECRET` values, so configure each
separately.

## Explicit non-goals

- Task 5b retries.
- Yandex Cloud Logging stdout/stderr.
- Router WebSocket and streaming features.
- A second cloud provider.

These remain separate items in the [task catalog](../README.md).
