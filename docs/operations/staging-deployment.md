# Staging deployment contract

Status: Suspended — no staging host since 2026-08-03; contract retained for its return
Type: Operations
Updated: 2026-08-12

Staging uses the same `infra/deploy.sh` and `infra/docker-compose.prod.yml` as
production, but shares no runtime resource with it.

## Suspended, not retired

There is currently no staging host: the temporary VDS was released and
production moved to its own machine. The `deploy-staging` job in
[pipeline.yml](../../.github/workflows/pipeline.yml) is therefore gated behind
the repository variable `STAGING_DEPLOY_ENABLED`, which is unset, so the job is
skipped instead of failing on every push.

The job was kept rather than deleted because staging exists to catch a bad
migration before it reaches real data, and that need returns with the first
real users. Everything else in the pipeline — tests, vet, lint, Terraform
validation, and image publishing to GHCR — keeps running; production deploys
depend on those images and would have nothing to install without them.

To bring staging back:

1. provision a host per [staging provisioning](staging-provisioning.md);
2. update the `staging` environment's `DEPLOY_SSH_HOST`,
   `DEPLOY_SSH_KNOWN_HOSTS`, and `DEPLOY_SSH_PRIVATE_KEY`, and check the
   remaining backend staging variables (`DEPLOY_SMOKE_URL`,
   `DEPLOY_COMPOSE_PROJECT`, and the domains they name);
3. set repository variable `STAGING_DEPLOY_ENABLED=true`.

Until then, a commit reaches production without ever having been deployed
anywhere else. Nothing enforces staging as a prerequisite — the production
workflow only checks that the SHA is an ancestor of `main` and that its images
exist — so the rehearsal is a practice, not a gate.

The Terraform backend, plan sequence, state-migration boundary, and VDS host
contract are defined in [staging provisioning](staging-provisioning.md).
Staging and production use dedicated state buckets and mutually inaccessible
backend identities; different keys in a shared bucket are forbidden.

## Required inventory

Create a dedicated non-production VDS with a non-root deployment account. The
account needs read/write access only to its `/opt/snaphost` tree and access to
the Docker daemon. Docker group membership is effectively root-equivalent on
that VDS, so do not reuse the account or SSH key for general administration.
It does not need sudo, Terraform credentials, DNS credentials, or interactive
root SSH.

The VDS must have its own:

- `/opt/snaphost/env/production.env` (mode `0400` or `0600`);
- builder key, runner key, and GHCR token under its own secrets directory;
- state, backup, release, and Docker data directories;
- Docker Compose project and named PostgreSQL, Redis, and BuildKit volumes;
- HTTPS control-plane and wildcard application domains/certificates;
- PostgreSQL database and credentials;
- Yandex folder, registry/repository names, API Gateway, router container,
  Lockbox secret, service accounts, DNS records, and resource-name prefix.

Its protected environment file sets
`CORS_ALLOW_ORIGINS=https://staging.kinocassa.ru`. Without this Compose value,
browser `OPTIONS` preflight reaches JWT middleware and fails with `401` before
the authenticated request is sent.

The minimum acceptable Yandex isolation is a separate folder with separate
service accounts, Lockbox secret, registry, gateway, DNS names, and resource
names. Production keys and secrets must never be copied into staging.

## GitHub Environment configuration

Create the `staging` Environment with these secrets (values are never stored in
Git):

- `DEPLOY_SSH_HOST`
- `DEPLOY_SSH_PRIVATE_KEY`
- `DEPLOY_SSH_KNOWN_HOSTS`

Configure these environment variables:

- `DEPLOY_SSH_USER` — dedicated non-root account;
- `DEPLOY_SSH_PORT` — normally `22`;
- `DEPLOY_ROOT` — staging root, normally `/opt/snaphost` on its own VDS;
- `DEPLOY_SMOKE_URL` — staging API gateway HTTPS base URL;
- `DEPLOY_GHCR_USERNAME` — package-read identity matching the protected token
  already installed on the VDS.
- `DEPLOY_COMPOSE_PROJECT` — stable lowercase Compose project name matching
  `^[a-z0-9][a-z0-9_-]{0,62}$`, for example `snaphost-staging`.

The `production` Environment uses the same names with entirely different
values, including a different stable project name such as
`snaphost-production`, and must have required reviewers. `known_hosts` contains a previously
verified host key obtained through an out-of-band provisioning process;
workflow-time `ssh-keyscan` is forbidden.

Frontend staging configuration lives in `justaba/snaphost-ui`, not in this
repository. Recreating staging therefore requires a second GitHub Environment
there with its own `DEPLOY_FRONTEND_URL`, `VITE_*` variables, frontend-only SSH
key, and non-root account. Never reuse the backend deploy key for the frontend
workflow or place a Supabase secret/service-role key in a Vite build.

## Release and deployment flow

The workflow verifies all six SHA-tagged GHCR manifests before SSH. It uploads
one archive to `releases/.incoming-<sha>-<run-id>.tgz`, extracts into a unique
candidate, refuses different content at an existing SHA release, preserves
`deploy.sh` as executable, and runs preflight against that release. It then
deploys from the explicit versioned release path. Only after deployment succeeds
does an atomic symlink rename update `current`; any preflight/deploy failure
leaves `current` pointing at the previous successful release.

No env file, service-account key, GHCR token, database backup, or Terraform
state is transferred. The GHCR token remains a mode-`0400`/`0600` VDS file used
by `deploy.sh` through `--password-stdin`.

Every Compose invocation includes `--project-name` from
`SNAPHOST_COMPOSE_PROJECT`. Release-directory changes therefore update one
stable stack and its existing named volumes instead of creating a stack per SHA.

## Staging frontend on the VDS

Caddy uses `infra/Caddyfile.staging.example` and serves the atomic
`/opt/snaphost/frontend/current` symlink. The standalone frontend workflow
uploads its artifact into `frontend/releases/<frontend-sha>`, switches the
symlink atomically, and runs an HTTPS smoke check. On failure it restores the
previous symlink. The backend staging workflow does not read or write that
tree.

For the current temporary VDS, `staging.kinocassa.ru` requires an explicit A
record to the VDS address, overriding the staging zone's wildcard gateway
record. The Caddy configuration is a one-time root-owned host prerequisite;
the non-root deployer only owns versioned static files and cannot change TLS or
proxy configuration.

Production currently uses the same host-Caddy delivery model with a separate
frontend-only identity. A CDN can be added later without coupling the two
repositories again.

## Remaining live work

The deleted temporary VDS last ran backend and frontend SHA
`2286b2da3f60329611488de23b8f060ceebb9d5b` from the former monorepository.
That shared SHA is historical only. Future staging records must capture one
backend SHA and one independent frontend SHA.
