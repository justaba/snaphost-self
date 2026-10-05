# Task 7 — Install and upgrade without us

**Status:** Done. Public `v0.1.0`/`v0.1.1` install, password rotation,
upgrade, guarded rollback and encrypted local SQLite restoration passed on
an amd64 VDS. The published image also built the representative React/Vite
application on an isolated 1 GiB amd64 guest without swap/OOM or interruption
of the control plane and existing site.
**Completed:** 2026-10-05
**Created:** 2026-08-29
**Updated:** 2026-10-05

## Goal

Someone who is not us installs this on their own machine, upgrades it, and rolls
it back when an upgrade goes wrong — without an SSH key we hold, a GitHub
environment we own, or a CD pipeline that reaches into their box.

This task implemented that installation contract. At the
start of this task it deployed *one specific machine*, `135.106.166.76`, from
CI over SSH: a job built an image tagged with a Git SHA, pushed it to a private
GHCR package, `scp`ed a release tarball to `/opt/snaphost/releases/<sha>/`,
moved a `current` symlink, and ran `deploy.sh` on the far end. Item 2 has
removed that path. `infra/snaphostctl` is now its portable replacement; its
real-host acceptance is now recorded below in addition to fake-command coverage.

## Why this is a task rather than a fix

The inherited deployment path is not broken because it was written badly — it
is a careful piece of work, with a lock, an ordered rollout, a pre-migration
backup, and a test suite that fakes every external command. It is broken
because it answers a question this product stopped asking.

Two defects found on 2026-08-29 are the evidence, and both had been live for
several commits with a green suite:

- `rollback_to` iterated the seven services deleted when the control plane
  became one process, so every rollback failed on its first `compose up`;
- `backup.sh` required table data for `wallets` and `transactions`, dropped
  with billing, so every scheduled backup would have been refused.

Neither was caught by a test. They were caught by reading the files, because
nobody runs this path: there is one machine, it is ours, and it is deployed by
a pipeline that only ever moves forward. A path exercised once a month by one
person is a path that rots. The fix is not more tests against fakes — it is a
deployment story someone actually performs.

## The version and registry model — settled 2026-09-03

Owner decision. Everything else in this task hangs off it, and none of it was
derivable from the code.

- **Semantic versioning, with per-SHA tags published alongside.** An operator
  upgrades to `v1.2.3`; `ghcr.io/<repo>/snaphost:<40-hex-sha>` keeps being
  published from `main` so a regression can still be bisected. The version is
  what `deploy.sh` validates and what the state files record; the SHA tag is a
  debugging affordance and never an upgrade target.

  The major number is where "this upgrade is not reversible" gets said out
  loud. That matters here because rollback across a migration is already
  refused without `MIGRATIONS_BACKWARD_COMPATIBLE`, and a date scheme cannot
  express the difference between a release that is safe to roll back and one
  that is not.

- **The GHCR package is public.** `docker login`, the token file and
  `GHCR_USERNAME` leave the install path entirely. Keeping it private would
  mean issuing every operator a credential and rotating it, which is most of
  the way back to not being self-hosted — and the image contains no secret:
  it is the same binary this repository builds in public CI.

- **No `latest` tag is published.** `validate_rendered_compose` keeps refusing
  a floating tag, so a rendered manifest always names one exact version and two
  installs of the same file cannot silently differ. What an operator wanted
  `latest` for is a command, not a tag: `upgrade` with no argument resolves the
  newest published version, prints it, and pins it into the env file.

- **The manifest arrives as a `git clone` at the version tag.** The operator
  owns a checkout at a stable path; upgrading is `git fetch` plus a checkout of
  the new tag. `docker-compose.prod.yml`, `buildkitd.prod.toml`, the AppArmor
  profile, the systemd units and `backup.sh` then move as one version with the
  image they describe, which is the same skew Task 1 avoided by embedding the
  panel in the binary. The cost is `git` as an install prerequisite and a
  checkout an operator can edit — the latter is a feature for a self-hosted
  product and a support hazard for us, and this repository has no support
  obligation.

## Work plan

1. [x] Settle the version and registry model above. Written down there.

2. [x] **Stop CI from deploying.** Deleted the `deploy-staging` job in
   [pipeline.yml](../../../.github/workflows/pipeline.yml),
   `.github/workflows/production-deploy.yml`,
   `.github/scripts/deploy-remote.sh` and
   `.github/scripts/test-cd-contract.sh`. The workflow no longer consumes
   `DEPLOY_SSH_*` secrets or GitHub deployment environments. The `image` job
   stays and gains a tag trigger.

   The `image` job gained the tag trigger: a push to `main` publishes the
   per-SHA tag, a matching version tag publishes the version tag and SHA, a pull
   request builds and publishes nothing. The job refuses a tag that is not
   `vMAJOR.MINOR.PATCH` rather than publishing something an operator cannot
   name. Making the package public is a repository setting the workflow cannot
   assert. On 2026-10-05, anonymous pulls of both release images proved the
   public package setting without an operator registry credential.

   On 2026-10-05, strict SemVer validation moved into
   `infra/resolve-image-tags.sh`, whose suite exercises valid and invalid refs.
   The numeric tag filter already uses GitHub's supported `[0-9]+` syntax.

   The `</dev/null` comment moved to `backup_database` in `deploy.sh`, where
   the hazard still lives, and was rewritten around the case that outlives SSH:
   a script being read from stdin by the shell running it, which is what
   `curl … | bash` is.

   The `DEPLOY_SSH_*` secrets and the `production` and `staging` GitHub
   environments still exist as repository settings. Nothing reads them now.

3. [x] **An `install` path.** `infra/snaphostctl install <version>` validates a
   clean checkout at the exact release tag, creates protected env/state/backup
   directories, derives the Docker socket group, installs the BuildKit
   AppArmor profile, starts the Compose-managed Caddy edge, installs database
   and TLS backup units plus a stable CLI copy, and prints the generated
   operator password. A failed pre-migration first
   deploy can resume without rewriting the protected env file. The first VPS
   rehearsal exposed self-rejection of the full env template and a four-CPU
   default on a two-CPU machine; the installer now covers both cases.
   A later 1.9 GiB VDS rehearsal found that the Compose default BuildKit
   memory limit was 4 GiB regardless of host size. Install and upgrade now pin
   a default at half host RAM, rounded down to 64 MiB and capped at 4 GiB;
   an explicit operator limit is preserved. Sizing uses Linux's actual RAM,
   including on a nominal 1 GiB VPS whose kernel reserves part of that RAM.

4. [x] **Shrink the environment surface.** The required set is three:
   `SNAPHOST_VERSION`, `SNAPHOST_CONTROL_DOMAIN` and `SNAPHOST_ACME_EMAIL`.
   As of 2026-10-04, AI generation is optional and defaults to disabled.
   Only an explicit `LLM_ENABLED=true` requires `OPENROUTER_API_KEY`; the
   application and installer enforce this together. A stored key alone
   does not opt in. Project Dockerfiles and built-in templates need no key.

   The initial reduction needed no Go changes because the other config
   packages already treat an
   empty value as unset — `envOrDefault`, `parseIntEnv` and `parseBoolEnv` each
   answer with their fallback. An unset variable therefore reaches the process
   as an empty string and gets the default, so removing a line from the
   required set was enough for those settings. Optional AI additionally
   required a Go configuration switch and a permanent error when neither a
   template nor an enabled provider can generate the Dockerfile.

   That is true only for variables passed *into* the container. The ones
   Compose interpolates itself — the image prefix, the published address and
   port, both CPU and memory pairs — would render an invalid manifest from an
   empty value, so those got defaults in `docker-compose.prod.yml` instead.
   Two of them were load-bearing: `ALLOWED_IMAGE_PREFIXES` must be non-empty
   while `STRICT_IMAGE_VALIDATION` is true, and the Go default for that flag is
   true, so an install whose env file omitted both would have been refused at
   startup.

   Every remaining variable in the manifest carries `:-` rather than a copied
   default. Compose warns on each unset variable, and forty warnings per
   command teaches an operator to ignore output; an empty default silences that
   without writing any number down twice.

   `RUN_MIGRATIONS` stopped being configurable and is hard-coded to `false`.
   Its Go default is `true`, so an env file that merely omitted the line got
   startup migrations back — two containers on one SQLite file, which is the
   thing the separate migrate step exists to prevent.

   Fixed on the way, because the env template promised it and the manifest did
   not deliver it: `DOMAIN_CNAME_TARGET` and `DOMAIN_A_RECORD_TARGET` are now
   passed to the container. Without them a production install refused every
   custom-domain attach with 503, whatever the operator configured.

   `.env.production.example` now has three required values; the
   rest are commented-out defaults with the reasoning next to them.

   The counts had already come down from 179 and fifty-nine before this item
   started, because ADR 0008 removed `WEBHOOK_SECRET`, `RUNNER_BACKEND` and
   `ALIAS_IDLE_GC_DAYS` along with the machinery that read them.

5. [x] **Rework `deploy.sh` into an operator CLI.** Most of its logic survives
   and changes meaning rather than disappearing — see the table below.

   The first slice landed on 2026-09-04. `preflight` and `deploy` now accept
   only strict `vMAJOR.MINOR.PATCH` versions; `latest`, versionless numbers,
   leading-zero components and SHA arguments are refused. Successful deploys
   persist the pinned version through atomic replacements of `production.env`
   and state, and rollback updates both. A guard refuses later operations if
   those files ever disagree. An existing installation may read its old
   `sha=` state once, preserve that exact image as the transition rollback
   target; the next successful release returns the installation to
   version-only state.

   The readiness defect was fixed in the same slice. `probe_internal` ran
   `compose run --entrypoint curl snaphost`, but the runtime image contained no
   `curl`; every real deploy therefore failed after migrations while the fake
   accepted the nonexistent entrypoint. The production service now owns a
   Docker healthcheck, the runtime image contains its `curl` client, and
   `deploy.sh` waits specifically for `healthy` rather than treating a merely
   running container as ready. The suite has explicit cross-file regression
   checks for that contract and the host-visible runtime network, and now
   covers 62 scenarios.

   The outer `snaphostctl` command now owns the host and Git lifecycle.
   `upgrade` fetches tags and `origin/main`, rejects dirty/skewed/downgrade/
   off-main targets, selects the newest strict SemVer when no target is given,
   checks out the target and invokes that release's deploy engine. A failed
   deploy restores the original checkout and AppArmor profile. `rollback`
   moves runtime, state, checkout, AppArmor and systemd together; its dry-run
   is read-only. The separate suite covers 35 scenarios, including occupied
   public ports, domain isolation and migration of a pre-edge env file.

6. [x] **How the manifest reaches the operator: a `git clone` at the version
   tag.** Settled with the version model above. Every path in `deploy.sh` and
   `backup.sh` and every instruction in the docs is written against a checkout
   the operator owns at a stable location, not against `releases/<sha>/`.

7. [x] **Remove repository-owned uptime.**
   The inherited scheduled workflow and probe were deleted because they
   defaulted to the repository owner's domains and opened incidents in this
   repository. Self-hosted operators now receive guidance to configure an
   independent monitor with operator-owned endpoints, credentials and alert
   delivery in [monitoring.md](../../operations/monitoring.md).

8. [x] **Docs.** [ci-cd.md](../../operations/ci-cd.md) is mostly deleted.
   [rollback.md](../../operations/rollback.md),
   [backups.md](../../operations/backups.md),
   [monitoring.md](../../operations/monitoring.md) and
   [deployment-model.md](../../architecture/deployment-model.md) are edited. A
   [install-and-upgrade.md](../../operations/install-and-upgrade.md) is the new
   product front door and explicitly records the Compose-managed edge and
   real-host proof boundary.

## What survives, what changes, what goes

| | |
| --- | --- |
| **Survives unchanged** | the `flock` lock shared with `backup.sh`; the dump before migrations; migrations as their own step ahead of the application; readiness plus the restart-loop check; the smoke checks; atomic state files and pulled-image digests; refusing to roll back across a migration without `MIGRATIONS_BACKWARD_COMPATIBLE` |
| **Changes meaning** | version validation (a tag, not 40 hex); how the previous version is resolved for rollback; where the Compose file lives — a stable path the operator owns, not `releases/<sha>/` |
| **Goes** | `docker login ghcr.io` with a token file; the `releases/<sha>/` layout and the `current` symlink, which are `deploy-remote.sh`'s model; `SNAPHOST_PUBLIC_SMOKE_URL` as a hard requirement — a box being installed may not have public HTTPS yet |

## Ordering

**After Task 1, not before.** That dependency is now satisfied; see the
[completed task](../completed/0001-collapse-to-one-binary.md#completion-and-next-task).
The application shape, embedded panel, SQLite store, memory-limit derivation
and production manifest are now settled inputs. This task can change the
release model and shrink the environment surface once instead of tracking a
moving multi-service collapse.

Completed [Task 4](../completed/0004-production-edge.md) provides the
application-side edge contract and Caddyfile. Its public DNS/ACME and local
TLS-state restore acceptance passed on 2026-10-05. Task 7 owns installing that
package from a published SemVer release, then upgrading and rolling it back on
a real host. The VDS edge rehearsal used a locally built image and therefore
does not satisfy this task's release-install criterion.

## Acceptance criteria

- A person with a fresh VPS, this repository's published image, and the install
  runbook has a working platform and can log into it — with no credential
  issued by us and no SSH access granted to us.
- Upgrade and rollback are both performed against a real machine, not only
  against fakes. The 2026-09-04 rehearsal proves the host mechanics against an
  isolated origin and registry; the 2026-10-05 acceptance below repeats this
  against the published GitHub/GHCR path.
- A restore drill against a SQLite dump produced by the documented local
  backup flow. Off-host object storage is an optional operator policy rather
  than an installation acceptance dependency.
- A representative Node application builds on the minimum supported host
  without OOM-killing the control plane or an already running site. Record host
  RAM, swap, BuildKit limit, peak usage and build duration.
- No SSH key, deploy secret, or GitHub environment is required to run the
  product.
- The shell test suites still pass, and their fakes still refuse what the real
  commands refuse — the property added on 2026-08-29 after both suites were
  found to be agreeing with themselves.

## Acceptance evidence — 2026-09-04

The [first VPS rehearsal](../../operations/rehearsals/2026-09-04-vps.md)
completed real install, login, local backup/restore, checkout-aware upgrade,
guarded rollback, and two successful Vite deploys. A measured cold build took
147 seconds on a 2-vCPU/4-GB/no-swap host, peaked at 997.2 MiB used, caused no
OOM or restart, and left an already running site serving.

The drill also found and fixed the env-template placeholder check, host CPU
sizing, the production network name, duplicate saga scheduling during a long
build, and retry state after a temporary runtime failure. It does not close the
task: it used a loopback registry and local Git origin because GitHub had no
SemVer release, restored a local plaintext dump, and did not exercise a 1 GB
host.

The [2026-10-05 VDS Node rehearsal](../../operations/rehearsals/2026-10-05-task7-vds-node.md)
completed a small Vite build on 1.9 GiB RAM without swap: 25.6 seconds,
791.5 MiB peak host RAM in use, 524.6 MiB peak BuildKit cgroup use, with the
control plane and an existing public project still serving and no restarts.
A Vite 6 plus React build exceeded 768 MiB and 1 GiB BuildKit limits.
The subsequent pinned React 19.2.4/Vite 8.0.4 fixture passed on the same VDS
in 9.3 seconds, with HTML and compiled JS both returning HTTP 200 and no OOM
or restart. This remains a locally built image rather than a public release.

The [isolated 1 GiB arm64 drill](../../operations/rehearsals/2026-10-05-task7-1g.md)
then exercised a fresh `/opt/snaphost` installation with the real production
Compose file, AppArmor and systemd. The installer selected a 448 MiB BuildKit
limit from `MemTotal=977868 KiB`. The same React/Vite fixture built in
9.7 seconds without swap, at 564.9 MiB peak host RAM in use, with no OOM or
restart and 11 successful health/existing-site probe pairs. An encrypted dump
from the installed backup service passed checksum and integrity checks, was
restored into the live installation, and supported login and password rotation.
The live DB had mode 0600 and UID/GID 1000:1000. No plaintext SQL artifact
or private-key log output remained.

This establishes the measured 1 GiB workload and local encrypted restore,
with an architecture boundary: the VM is arm64 while published CI images
currently target amd64. It still used a local tag and loopback registry.
At that point, public GitHub/GHCR install, upgrade and rollback were still
required before Done; the final acceptance below supplies that proof.
The [release record](../../operations/release-candidate-task7.md) documents
the published version pair. The pending statements in this preparation
section describe the boundary before publication; the acceptance below closes it.

## Final public-release acceptance — 2026-10-05

The [public-release report](../../operations/rehearsals/2026-10-05-task7-public-release.md)
contains commands, immutable commits, image digests, CI links and sanitized
measurement/audit artifacts. All task criteria passed:

| Criterion | Recorded proof |
| --- | --- |
| Fresh install without owner-issued credentials | anonymous public `v0.1.0` install on the VDS and `v0.1.1` on a fresh amd64 guest; fresh login 200, password rotation 204, old password 401, new login 200 |
| Actual upgrade and rollback | VDS `v0.1.0` → `v0.1.1` → `v0.1.0` → `v0.1.1`; Git/env/state/OCI revision aligned, account/projects retained, rollback guard refused without compatibility confirmation, dry-run left files/HEAD/containers unchanged |
| Documented local backup restore | installed systemd service produced an age-encrypted dump; checksum OK, live replacement, integrity OK, schema 2 clean, rows retained, DB mode 0600 and UID/GID 1000:1000, login/control/project HTTPS 200 |
| Representative Node on minimum host | public amd64 image, 1 GiB/2 vCPU guest, no swap, BuildKit 448 MiB, 661.6 MiB peak host RAM, 54.4 s under QEMU, compiled HTML/JS 200, 33 successful health/site probe pairs, no OOM/restart |
| No owner infrastructure dependency | public Git tag checkout and GHCR pulls, no SSH deploy job/GitHub environment/registry login required on an operator host |
| Verification remains green | main and both release workflows passed Go/panel/shell/image jobs, including real Caddy integration, Compose rendering and pinned Caddyfile validation |

Only Caddy publishes 80/443 and has no Docker socket. Both public leaf
certificate fingerprints survived install, upgrade, rollback and DB restore;
Caddy recorded zero new orders during the final run. Backup storage is local
by the operator's chosen scope. Public images target amd64; large builds,
preview TTL, absent project volumes/managed databases and optional off-host
recovery remain stated limits rather than missing Task 7 evidence.

## Out of scope

Multi-host installs, an updater that runs inside the product, and any package
format (deb, Helm, one-line `curl | sh`). One documented procedure against
Docker Compose is the whole of this task.
