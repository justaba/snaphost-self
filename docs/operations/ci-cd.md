# CI/CD contract

Status: Current
Type: Operations
Updated: 2026-08-12

## Linting

Until 2026-08-07 no workflow ran a linter at all. `make lint` covered three of
the seven Go modules, there was no `.golangci.yml`, and golangci-lint's default
set excludes `gofmt` — so formatting drift and unchecked errors accumulated
without anything failing.

The `go` matrix now runs `golangci-lint` for every module, including the
`yandex`-tagged runner build. Configuration is
[snaphost-backend/.golangci.yml](../../snaphost-backend/.golangci.yml), which
the tool finds by walking up from each module directory — the services are
separate modules, so one file at that level covers all of them.

The version is pinned to **v1.64.8** in both the workflow and the documented
local requirement. The v2 config schema is incompatible with that file; moving
to it means changing the config and the pin in the same commit, or CI and
workstations stop agreeing about what passes.

This document records the CI/CD scope implemented in July 2026. The active CI
and staging workflow is
`.github/workflows/pipeline.yml`; the old `.github/workflows/ci.yml` has no
triggers and is retained only because the workspace ACL prevented deletion.
Manual production deployment is defined by
`.github/workflows/production-deploy.yml`.

## Problem to solve

The previous workflow represented the old three-service architecture. It did
not run Go tests or lint checks, omitted `user-billing`, `ai-orchestrator`,
`router-svc`, and `shared`, and used invalid Docker contexts for services that
depend on the shared Go module.

Its SSH step referenced nonexistent Compose services named `builder-svc` and
`runner-svc`. Local Compose instead has API/worker pairs and uses `build:`
rather than the GHCR images produced by CI. The step could therefore report a
deployment without deploying the triggering commit.

## Implemented

Every pull request and push to `main` now runs:

- `go test ./...`, `go vet ./...`, and `golangci-lint run` for every Go module;
- an additional runner pass with the `yandex` build tag;
- Terraform formatting, initialization without a backend, and validation;
- Docker builds for all six deployable backend images using correct contexts;
- a Yandex-enabled production runner image build.

After a successful push to `main`, the six backend images are published to GHCR
with immutable commit SHA tags. Pull requests do not publish or deploy
anything. Frontend validation and artifacts are owned by the separate
[`justaba/snaphost-ui`](https://github.com/justaba/snaphost-ui) repository.

## Backend CD contract

After all six `images` matrix entries succeed on a push to `main`, the staging
job verifies that all six GHCR manifests exist at `github.sha`, prepares an
immutable release directory on the staging VDS, runs remote preflight, switches
to the exact release for deployment, and updates the `current` symlink
atomically only after success. Pull requests run CI but cannot reach the staging
job.

Production is a separate `workflow_dispatch` workflow. It rejects non-40-hex
inputs, checks out that exact SHA, and requires it to be an ancestor of
`origin/main` before image verification or SSH. A branch/PR-only commit cannot
be deployed even if an image happens to exist. The job verifies the same six
manifests and uses the `production` GitHub Environment. Required reviewers on
that Environment are an external GitHub setting and must be configured before
enabling production.

Both deployment jobs use environment-scoped concurrency groups with
`cancel-in-progress: false`; a newer run queues rather than cancelling an active
deployment. The top-level CI cancellation policy only cancels pull-request
runs, not push/staging deployments.

Each GitHub Environment supplies a stable `DEPLOY_COMPOSE_PROJECT`. The remote
helper validates it and maps it to `SNAPHOST_COMPOSE_PROJECT`; changing release
SHA never changes the Compose stack identity.

Native OpenSSH uses a pre-verified `known_hosts` environment secret with
`StrictHostKeyChecking=yes` and never calls `ssh-keyscan`. The remote account is
validated as non-root. The workflow transfers only `deploy.sh`,
`docker-compose.prod.yml`, and `buildkitd.prod.toml`. Production/staging env,
GHCR token, and Yandex keys already exist as protected files on their respective
VDS and never cross the SSH command line.

## Required repository settings

- GitHub Actions needs repository package write permission.
- Create separate `staging` and `production` GitHub Environments with the
  environment-scoped deployment values listed in
  [staging deployment](staging-deployment.md). Configure required reviewers
  when the repository plan supports protected Environments.
- Branch protection should require Terraform, all Go matrix checks, all Image
  matrix checks, and Deployment scripts.

As of 2026-08-12, the current private-repository plan exposes neither required
reviewers nor branch protection through GitHub's API. Exact-SHA validation and
manual production dispatch remain active safeguards, but they are not a
substitute for an independent reviewer. Enable those protections after a plan
upgrade.

Repository-side workflows do not create VDS, GitHub settings, DNS, firewall, or
Yandex resources and never run Terraform apply.

## Frontend CD boundary

The frontend repository builds with its own environment-specific `VITE_*`
variables and deploys an exact frontend SHA through a dedicated non-root SSH
identity. It owns `/opt/snaphost/frontend/` only. This repository's deployment
identity continues to own backend releases, Docker, protected env files, and
database backups. A backend workflow must never update the frontend symlink,
and a frontend workflow must never restart backend containers.

Production records the two SHAs independently. A breaking API change is
released additively in the backend first, then consumed by the frontend; the
old API shape stays available while an older frontend release remains a valid
rollback target.
