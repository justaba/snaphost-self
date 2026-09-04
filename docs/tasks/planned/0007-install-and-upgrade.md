# Task 7 — Install and upgrade without us

**Status:** In progress. The version and registry decision below is settled as
of 2026-09-03, which unblocks the rest of the work plan.
**Created:** 2026-08-29
**Updated:** 2026-09-04

## Goal

Someone who is not us installs this on their own machine, upgrades it, and rolls
it back when an upgrade goes wrong — without an SSH key we hold, a GitHub
environment we own, or a CD pipeline that reaches into their box.

That is what "self-hosted" means and the repository does not do yet. At the
start of this task it deployed *one specific machine*, `135.106.166.76`, from
CI over SSH: a job built an image tagged with a Git SHA, pushed it to a private
GHCR package, `scp`ed a release tarball to `/opt/snaphost/releases/<sha>/`,
moved a `current` symlink, and ran `deploy.sh` on the far end. Item 2 has
removed that path; a supported replacement is still being built here.

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
   [production-deploy.yml](../../../.github/workflows/production-deploy.yml),
   [deploy-remote.sh](../../../.github/scripts/deploy-remote.sh) and
   [test-cd-contract.sh](../../../.github/scripts/test-cd-contract.sh), plus the
   `DEPLOY_SSH_*` secrets and both GitHub environments. The `image` job stays
   and gains a tag trigger.

   The `image` job gained the tag trigger: a push to `main` publishes the
   per-SHA tag, a `v*` tag publishes the version tag and the SHA, a pull
   request builds and publishes nothing. The job refuses a tag that is not
   `vMAJOR.MINOR.PATCH` rather than publishing something an operator cannot
   name. Making the package public is a repository setting the workflow cannot
   assert; until it is done, every install still needs a credential.

   The `</dev/null` comment moved to `backup_database` in `deploy.sh`, where
   the hazard still lives, and was rewritten around the case that outlives SSH:
   a script being read from stdin by the shell running it, which is what
   `curl … | bash` is.

   The `DEPLOY_SSH_*` secrets and the `production` and `staging` GitHub
   environments still exist as repository settings. Nothing reads them now.

3. [ ] **An `install` path, which does not exist today.** The production box was
   brought up by hand, so there is no first-run story at all: create the
   directories, write an env file from a template, pull, migrate, start, and print
   the operator password once. Item 6a of Task 1 already generates that
   password; this is the same idea applied to the rest of the install.

4. [x] **Shrink the environment surface.** `preflight` requires three variables
   where it required forty-five: `SNAPHOST_VERSION`, `DOMAIN_SUFFIX` and
   `OPENROUTER_API_KEY`. Nothing else has a value that could only come from an
   operator.

   **No Go changed, and the reason is worth recording.** The item assumed this
   would touch the config packages; it did not, because they already treat an
   empty value as unset — `envOrDefault`, `parseIntEnv` and `parseBoolEnv` each
   answer with their fallback. An unset variable therefore reaches the process
   as an empty string and gets the default, so removing a line from the
   required set is enough on its own.

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

   `.env.production.example` is now 129 lines of which three are decisions; the
   rest are commented-out defaults with the reasoning next to them.

   The counts had already come down from 179 and fifty-nine before this item
   started, because ADR 0008 removed `WEBHOOK_SECRET`, `RUNNER_BACKEND` and
   `ALIAS_IDLE_GC_DAYS` along with the machinery that read them.

5. [ ] **Rework `deploy.sh` into an operator CLI.** Most of its logic survives
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
   running container as ready. The suite has an explicit cross-file regression
   check for that contract and now covers 52 scenarios.

   Still missing from this item: the `upgrade` command which fetches and checks
   out a release tag (including no-argument newest-version resolution), and its
   integration with the first-install path. Those cannot be honestly called
   complete until exercised on a real host.

6. [x] **How the manifest reaches the operator: a `git clone` at the version
   tag.** Settled with the version model above. Every path in `deploy.sh` and
   `backup.sh` and every instruction in the docs is written against a checkout
   the operator owns at a stable location, not against `releases/<sha>/`.

7. [ ] **Decide what happens to the uptime workflow.**
   [uptime.yml](../../../.github/workflows/uptime.yml) probes the box every ten
   minutes and opens one GitHub issue per incident, in this repository. That is
   a vendor watching their own machine. An operator's incidents are not our
   issues.

   Still open, and now inconsistent: item 2 removed the pipeline that deployed
   the host this workflow watches, and `uptime-check.sh` still defaults to that
   host's three domains. It probes a machine this repository no longer ships
   to. Deleting both is the reading this item argues for; what replaces it for
   an installed host is the guidance already in
   [monitoring.md](../../operations/monitoring.md) — an independent monitor,
   because GitHub's schedules are best-effort and disabled after inactivity.

8. [ ] **Docs.** [ci-cd.md](../../operations/ci-cd.md) is mostly deleted.
   [rollback.md](../../operations/rollback.md),
   [backups.md](../../operations/backups.md),
   [monitoring.md](../../operations/monitoring.md) and
   [deployment-model.md](../../architecture/deployment-model.md) are edited. A
   new install-and-upgrade runbook is written, and it is the product's front
   door — there is nothing occupying that position now.

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

Task 4 may still change the edge artifacts, but that is not a reason to keep
installation blocked: Task 7 must define an explicit edge prerequisite and can
hand the final integrated Caddy configuration to Task 4 when it lands.

## Acceptance criteria

- A person with a fresh VPS, this repository's published image, and the install
  runbook has a working platform and can log into it — with no credential
  issued by us and no SSH access granted to us.
- Upgrade and rollback are both performed against a real machine, not only
  against fakes. The 2026-08-03 rehearsal is the last one that happened, it
  predates the collapse to one service and SQLite, and the path it exercised
  was broken shortly afterwards.
- A restore drill against a SQLite dump, which has never been run.
- A representative Node application builds on the minimum supported host
  without OOM-killing the control plane or an already running site. Record host
  RAM, swap, BuildKit limit, peak usage and build duration.
- No SSH key, deploy secret, or GitHub environment is required to run the
  product.
- The shell test suites still pass, and their fakes still refuse what the real
  commands refuse — the property added on 2026-08-29 after both suites were
  found to be agreeing with themselves.

## Out of scope

Multi-host installs, an updater that runs inside the product, and any package
format (deb, Helm, one-line `curl | sh`). One documented procedure against
Docker Compose is the whole of this task.
