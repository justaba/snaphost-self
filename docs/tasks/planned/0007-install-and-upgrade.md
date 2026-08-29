# Task 7 — Install and upgrade without us

**Status:** Planned. Nothing started. Blocked on one decision (below), and
deliberately ordered after Task 1 finishes.
**Created:** 2026-08-29
**Updated:** 2026-08-30

## Goal

Someone who is not us installs this on their own machine, upgrades it, and rolls
it back when an upgrade goes wrong — without an SSH key we hold, a GitHub
environment we own, or a CD pipeline that reaches into their box.

That is what "self-hosted" means and the repository does not do it. What it does
today is deploy *one specific machine*, `135.106.166.76`, from CI over SSH:
a job builds an image tagged with a Git SHA, pushes it to a private GHCR
package, `scp`s a release tarball to `/opt/snaphost/releases/<sha>/`, moves a
`current` symlink, and runs `deploy.sh` on the far end. Every piece of that
assumes the operator and the publisher are the same party.

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

## The decision this is blocked on

**The version and registry model.** Everything else hangs off it and it is not
derivable from the code:

- **Version scheme.** Today an image is `ghcr.io/<repo>/snaphost:<40-hex-sha>`
  and `deploy.sh` validates exactly that shape. An operator upgrades to a
  *version*, not to a commit. Semantic versioning, a date scheme, or something
  else — and whether per-SHA tags keep being published alongside, which is
  cheap and makes bisecting a regression possible.
- **Registry visibility.** The GHCR package is private and `deploy.sh` logs in
  with a token file. A public package removes the login and the token from
  every install; keeping it private means every operator needs credentials
  from us, which is most of the way back to not being self-hosted.
- **A `latest` tag, or not.** `validate_rendered_compose` currently refuses
  one outright, and that refusal is correct for a pinned production rollout.
  It is less obviously correct for an operator who wants `docker compose pull`
  to mean something.

Until these are settled the rest cannot be written, because the version format
decides the validation, the rollback target resolution, and the CI trigger.

## Work plan

1. [ ] Settle the version and registry model above. Write it down here.

2. [ ] **Stop CI from deploying.** Delete the `deploy-staging` job in
   [pipeline.yml](../../../.github/workflows/pipeline.yml),
   [production-deploy.yml](../../../.github/workflows/production-deploy.yml),
   [deploy-remote.sh](../../../.github/scripts/deploy-remote.sh) and
   [test-cd-contract.sh](../../../.github/scripts/test-cd-contract.sh), plus the
   `DEPLOY_SSH_*` secrets and both GitHub environments. The `image` job stays
   and gains a tag trigger.

   `deploy-remote.sh` carries one thing worth not losing: the comment
   explaining why `</dev/null` is load-bearing when a script is fed to
   `ssh host bash -s`. Move it rather than delete it — the same hazard exists
   wherever an operator pipes an install script to a shell.

3. [ ] **An `install` path, which does not exist today.** The production box was
   brought up by hand, so there is no first-run story at all: create the
   directories, **generate `WEBHOOK_SECRET`** rather than asking for it to be
   typed, write an env file from a template, pull, migrate, start, and print
   the operator password once. Item 6a of Task 1 already generates that
   password; this is the same idea applied to the rest of the install.

4. [ ] **Shrink the environment surface.** `.env.production.example` is 166
   lines and `preflight` requires fifty-nine variables. That is a SaaS
   operator's configuration file, not an installer's. Most already have sane
   defaults in the Go config packages — the work is deciding which are genuinely
   an operator's business (domain, an OpenRouter key, generated secrets) and
   removing the rest from the required set. This touches Go, not only shell.

5. [ ] **Rework `deploy.sh` into an operator CLI.** Most of its logic survives
   and changes meaning rather than disappearing — see the table below. The
   513-line script and its 519-line test suite are reshaped together.

6. [ ] **Decide how the manifest reaches the operator.** Today
   `docker-compose.prod.yml`, `buildkitd.prod.toml`, the AppArmor profile, the
   Caddyfile and the systemd units arrive inside a release tarball or are
   installed by hand from a checkout. Clone the repository, or download two
   files? It changes every path in `deploy.sh` and `backup.sh` and every
   instruction in the docs.

7. [ ] **Decide what happens to the uptime workflow.**
   [uptime.yml](../../../.github/workflows/uptime.yml) probes the box every ten
   minutes and opens one GitHub issue per incident, in this repository. That is
   a vendor watching their own machine. An operator's incidents are not our
   issues.

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
| **Survives unchanged** | the `flock` lock shared with `backup.sh`; the dump before migrations; migrations as their own step ahead of the application; readiness plus the restart-loop check; the smoke checks; atomic state files; refusing to roll back across a migration without `MIGRATIONS_BACKWARD_COMPATIBLE` |
| **Changes meaning** | version validation (a tag, not 40 hex); how the previous version is resolved for rollback; where the Compose file lives — a stable path the operator owns, not `releases/<sha>/` |
| **Goes** | `docker login ghcr.io` with a token file; recording image digests for an SSH-delivered release; the `releases/<sha>/` layout and the `current` symlink, which are `deploy-remote.sh`'s model; `SNAPHOST_PUBLIC_SMOKE_URL` as a hard requirement — a box being installed may not have public HTTPS yet |

## Ordering

**After Task 1, not before** — after its items 8, 10 and 9, in that order
(see [Order of the remaining items](../active/0001-collapse-to-one-binary.md#order-of-the-remaining-items)).
Both of them touch the same three files this task rewrites, and item 7 already
did — it removed Redis from the manifest on 2026-08-30, taking `INFRA_SERVICES`,
`EXPECTED_SERVICES`, the health dependencies and both test suites with it:

- item 8 sets `GOMEMLIMIT` and container memory limits, which are Compose and
  environment changes;
- item 10 embeds the panel, which adds a Node stage to the image build and may
  change how it is tagged.

Doing this first means rewriting `docker-compose.prod.yml`, `deploy.sh` and
`deploy_test.sh` twice more. Work plan item 4 below has the same problem
from the other end: shrinking the environment surface wants a settled set of
variables, and item 8 is still adding one.

The argument against waiting is that the product stays uninstallable by anyone
else for longer. It is weaker than it looks: finishing this before Task 1's
item 10 would deliver an install procedure for a platform with no interface.

## Acceptance criteria

- A person with a fresh VPS, this repository's published image, and the install
  runbook has a working platform and can log into it — with no credential
  issued by us and no SSH access granted to us.
- Upgrade and rollback are both performed against a real machine, not only
  against fakes. The 2026-08-03 rehearsal is the last one that happened, it
  predates the collapse to one service and SQLite, and the path it exercised
  was broken shortly afterwards.
- A restore drill against a SQLite dump, which has never been run.
- No SSH key, deploy secret, or GitHub environment is required to run the
  product.
- The shell test suites still pass, and their fakes still refuse what the real
  commands refuse — the property added on 2026-08-29 after both suites were
  found to be agreeing with themselves.

## Out of scope

Multi-host installs, an updater that runs inside the product, and any package
format (deb, Helm, one-line `curl | sh`). One documented procedure against
Docker Compose is the whole of this task.
