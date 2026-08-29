# Production deployment

Status: Backend automation live; frontend independently deployed
Type: Operations
Updated: 2026-08-12

Phase 1 provides `infra/docker-compose.prod.yml` and
`infra/.env.production.example`. The manifest consumes only versioned images.
Do not use the local `infra/docker-compose.yml` in production.

Copy the env template outside version control and replace every placeholder.
`SNAPHOST_VERSION` must be the immutable 40-character Git SHA published by CI;
`GHCR_IMAGE_PREFIX` is the path before each image name. Builder and runner key
paths must reference external `0600` files. The builder key is mounted only in
`builder-worker`; the runner key only in `runner-api` and `runner-watchdog`.
Never mount Terraform bootstrap credentials.

`YANDEX_BUILDER_SA_ID` and `YANDEX_RUNNER_SA_ID` must name the service accounts
those two keys actually belong to. Preflight compares them against each key's
`service_account_id` and refuses to deploy on a mismatch, so another
environment's credentials stop the rollout rather than failing later as a
registry `403`. Where each environment's keys come from is in
[credentials.md](credentials.md).
`CORS_ALLOW_ORIGINS` is mandatory and contains an explicit comma-separated
allowlist of browser origins. Staging currently permits only
`https://staging.kinocassa.ru`; wildcard origins are not part of the contract.

## Domains and their manual steps

Production runs on two registrable domains — `snaphost.ru` for the dashboard
and API, `snaphost.pw` for user deploys and the custom-domain edge. The
reasoning and the security properties that depend on it are in
[deployment-model.md](../architecture/deployment-model.md); the reserved
address is in [public-address.md](public-address.md).

Terraform owns both DNS zones ([dns.tf](../../terraform/yandex/dns.tf),
[dns_control_plane.tf](../../terraform/yandex/dns_control_plane.tf)) from
`domain_name`, `control_plane_domain`, and `edge_ip_address`. The following are
*not* Terraform's and must be done by hand, in this order, before the dashboard
works on a new domain:

Where DNS is hosted is independent of where the server is: the VDS is at one
provider, the zones are in Yandex Cloud DNS, and the records simply point at the
server's address. The two domains are there for different reasons, which matters
if either is ever moved:

- `snaphost.pw` **needs** Yandex DNS. Its wildcard certificate is issued by
  Yandex Certificate Manager and validated with a `DNS_CNAME` challenge that
  Terraform writes into that zone ([certificate.tf](../../terraform/yandex/certificate.tf)).
  Hosting the zone elsewhere means carrying that record by hand and keeping it
  alive across automatic renewals.
- `snaphost.ru` is there **by choice**, so every record lives in one place. It
  needs nothing from Yandex: the apex, `www`, and `api` point at the application
  host, while `auth` points at the dedicated Supabase VDS; Caddy obtains the
  certificates itself from Let's Encrypt. Moving this zone to the
  registrar or any other DNS provider is a supported option — set
  `control_plane_domain = ""` so Terraform stops owning a zone nobody uses.

1. **Delegate the zones.** Set each domain's NS records at its registrar to the
   name servers Yandex assigns; the provider does not export them, so read them
   from the DNS zone in the console. Until delegation propagates, nothing
   resolves.
2. **Supabase.** A fresh project needs four things, in this order — the first
   two are SQL from the standalone
   [`justaba/snaphost-supabase`](https://github.com/justaba/snaphost-supabase/tree/main/supabase/migrations)
   repository, and the rest are dashboard settings:
   1. run `0001_profiles_and_hooks.sql` — `public.profiles`, the
      `on_auth_user_created` trigger that mirrors `auth.users`, RLS policies,
      and `custom_access_token_hook`;
   2. enable Integrations → Database Webhooks (this is what creates the
      `supabase_functions` schema), then run
      `0002_user_billing_webhook.sql` with its two placeholders replaced by the
      gateway URL and that environment's `SUPABASE_WEBHOOK_SECRET`;
   3. Authentication → Hooks → Customize Access Token (JWT) Claims → **Postgres**
      hook → `public.custom_access_token_hook`. Without it every token lacks
      `snaphost_role` and api-gateway falls back to `user` for everyone,
      including admins;
   4. Authentication → URL Configuration → Site URL `https://snaphost.ru` and
      redirect `https://snaphost.ru/auth/callback`. Missing this breaks login
      and the OAuth callback with no server-side error.

   Steps 2 and 3 fail silently in opposite directions: a webhook secret
   mismatch leaves new users with no wallet, and a missing hook silently
   demotes everyone. Verify both after the first signup — a row in
   `public.profiles`, and a matching row in `wallets` on the control plane.
3. **Frontend contract.** The standalone `justaba/snaphost-ui` production
   Environment uses `VITE_API_URL=https://api.snaphost.ru`; it is baked into
   the bundle and can only be changed by a new frontend release.
4. **`CORS_ALLOW_ORIGINS=https://snaphost.ru`** in the production env. Only the
   dashboard's own origin; never a deploy host.
5. **TLS.** Caddy on the host terminates TLS for the apex, `www`, and `api`
   labels of the control-plane domain. The wildcard certificate in Certificate
   Manager covers only `*.snaphost.pw` and cannot serve these names.
6. **Public Suffix List.** Submit `snaphost.pw` so browsers isolate one user's
   deploy from another's cookies. Outstanding; `router-svc` strips wide
   `Set-Cookie` domains as a stopgap, which does not cover cookies set by page
   JavaScript.

Order matters for the deploy domain in particular: the wildcard certificate
validates through a `DNS_CNAME` challenge written into the Yandex zone, so
`snaphost.pw` must be delegated to Yandex's name servers **before**
`terraform apply`, or the certificate sits in `Validating` and the API Gateway
binding never completes.

Validate without starting containers:

```bash
docker compose \
  --env-file infra/.env.production.example \
  -f infra/docker-compose.prod.yml \
  config
```

Expected services are `api-gateway`, `user-billing`, `builder-api`,
`builder-worker`, `runner-api`, `runner-watchdog`, `router-svc`,
`ai-orchestrator`, `postgres`, `redis`, and `buildkitd`. There must be no
`build:`, local Registry, Traefik, Docker socket, bootstrap key, `latest`, or
infrastructure host ports.

`router-svc` is the custom-domain ingress ([ADR 0007](../decisions/0007-custom-domain-tls-edge.md)).
It publishes `127.0.0.1:${ROUTER_BIND_PORT}` — loopback only, because it
forwards whatever `Host` it is handed — and the on-host Caddy proxies customer
domains to it. Deploys on generated `*.snaphost.pw` hostnames do not pass
through it; they go through the Yandex API Gateway to the Yandex-hosted
instance of the same image.

The target contract is described in
[architecture/deployment-model.md](../architecture/deployment-model.md). The
implementation plan and acceptance criteria are in
[Task 11](../tasks/active/0011-production-deployment.md).

This is not yet authorization or a procedure to deploy. Phase 1 excludes host
provisioning, TLS/firewall setup, image pull, migration sequencing, smoke tests,
rollback, backups, staging, and CD. The router lookup endpoint is implemented,
but its durable public HTTPS URL, certificate, firewall policy, rate limiting,
and live Yandex-to-VDS connectivity still require operator verification. Set
Terraform `router_user_billing_url` to the public control-plane/API gateway base
URL, without `/internal/routes`; router appends that exact path. Never point it
at user-billing directly or at a temporary smoke VDS. See the
[deployment model](../architecture/deployment-model.md).

Configured healthcheck commands match the utilities present in the known
PostgreSQL, Redis, BuildKit, and builder images, and the manifest passes Compose
config validation. Containers were not started during Phase 1, so runtime
health transitions are not yet verified.

`SCAN_FAIL_ON_CRITICAL=true` makes the build pipeline fail when the scanner
finds CRITICAL vulnerabilities, and the failed build event prevents the runner
from starting that image. However, registry cleanup currently supports only the
local Docker Registry v2 path. The rejected image may remain in Yandex
Container Registry: Yandex image deletion is a separate item in the
[backlog](../tasks/backlog.md), not implemented production cleanup.

## Host layout and permissions

The Linux VDS deployment user needs Docker access and these paths by default:

```text
/opt/snaphost/infra/docker-compose.prod.yml
/opt/snaphost/infra/buildkitd.toml
/opt/snaphost/env/production.env
/opt/snaphost/secrets/builder-key.json
/opt/snaphost/secrets/runner-key.json
/opt/snaphost/secrets/ghcr-token
/opt/snaphost/state/
/opt/snaphost/backups/
```

`production.env`, builder/runner keys, and the GHCR token must be regular files,
not symlinks, with mode `0600` or read-only `0400`. Any group/other permission,
including `0640` and `0644`, fails preflight.
The VDS deployment user and the non-root builder image use UID/GID `1000:1000`,
allowing the worker to read its bind-mounted `0400/0600` key without making the
secret group/world-readable or running the container as root. Host provisioning
must create the deployment user with that numeric UID/GID.
State and backup directories must be writable only by the deployment operator.
Override paths with `SNAPHOST_COMPOSE_FILE`, `SNAPHOST_ENV_FILE`,
`SNAPHOST_STATE_DIR`, `SNAPHOST_BACKUP_DIR`,
`SNAPHOST_BUILDER_KEY_FILE`, `SNAPHOST_RUNNER_KEY_FILE`, and
`SNAPHOST_GHCR_TOKEN_FILE`. Set `SNAPHOST_PUBLIC_SMOKE_URL` to the durable HTTPS
control-plane URL. HTTP is rejected unless `SNAPHOST_ALLOW_HTTP_SMOKE=true` is
explicitly set for an isolated test.

Set `SNAPHOST_COMPOSE_PROJECT` to a stable lowercase name for the environment.
It must not contain the release SHA. All Compose calls pass it through
`--project-name`, preserving the same networks and named volumes across release
directories. Staging and production should use different project names.

### Ubuntu 24.04 rootless BuildKit prerequisite

Ubuntu 24.04 restricts unprivileged user namespaces through AppArmor. Before
starting the production Compose project, a root operator must install and load
the repository's narrowly scoped BuildKit profile:

```bash
sudo install -o root -g root -m 0644 \
  infra/apparmor/snaphost-buildkit-rootless \
  /etc/apparmor.d/snaphost-buildkit-rootless
sudo apparmor_parser -r /etc/apparmor.d/snaphost-buildkit-rootless
sudo apparmor_status | grep -F snaphost-buildkit-rootless
```

Compose assigns this named profile only to `buildkitd`. Do not replace it with
`apparmor=unconfined` and do not disable Ubuntu's global unprivileged-userns
restriction. Host provisioning, not the non-root CD deployer, owns profile
installation. Deployment readiness will fail closed if the profile is absent
or not loaded.

The rootless BuildKit container also uses `seccomp=unconfined` and
`systempaths=unconfined`, as required for its nested OCI executor. The latter
allows the executor to mount its own procfs instead of inheriting Docker's
masked/read-only system paths. These exceptions apply only to `buildkitd`; it
continues to run as UID/GID `1000:1000` under the named AppArmor profile.

Builder-worker scans pushed Yandex images remotely because it has no Docker or
containerd socket. Immediately before each Trivy scan it obtains a short-lived
IAM token from the existing builder service-account key and creates a temporary
mode-`0600`, host-scoped Docker `config.json`. `DOCKER_CONFIG` is set only for
that Trivy child process and the directory is removed afterward. Tokens must
never be placed in command arguments, logs, Compose environment, or state.

## Commands

```bash
infra/deploy.sh preflight <40-character-git-sha>
infra/deploy.sh --dry-run deploy <40-character-git-sha>
infra/deploy.sh deploy <40-character-git-sha>
MIGRATIONS_BACKWARD_COMPATIBLE=true infra/deploy.sh rollback
```

The dry run validates inputs and prints action names without login, pull,
backup, Compose mutation, smoke requests, or deployment-state writes. The
rollback dry run may read existing current/previous state but creates no
directories, lock, or state files. The script never renders Compose to stdout
because rendered configuration contains secrets.

## Deployment sequence

The script acquires `flock`, refuses an existing `in-progress.env`, validates
the SHA/config/files/permissions/disk space and HTTPS URL, optionally logs into
GHCR using `--password-stdin`, and pulls target plus recorded rollback images
before changing runtime state. It intentionally does not log out: Docker
credentials may be shared by concurrent production tooling, so credential
isolation and lifecycle belong to the deployment user's Docker config.

For an existing database it creates an atomic custom-format PostgreSQL backup.
It then starts/checks PostgreSQL, Redis, and BuildKit; runs the two profile-only
migration jobs; updates user-billing, AI, builder, runner, and finally API
gateway; checks readiness; and runs smoke tests. It never uses `down`, deletes
volumes, or prunes images/backups.
API Gateway must pass a bounded internal HTTP `/health` probe after container
start and before any public Caddy/TLS smoke, preventing a
running-but-not-ready race.

The public smoke checks `/health`, then verifies that authenticated lookup for
a guaranteed-absent host returns 404 and an incorrect secret returns 401. It
does not create a user deploy. Internal HTTP readiness uses the builder image's
curl as a one-shot probe on the Compose networks; application images are not
modified to add probe tools. Worker/watchdog stability is checked by running
state and unchanged restart count.

## Migration and state limitations

Production API containers have `RUN_MIGRATIONS=false`. The `migration` Compose
profile provides `/migrate` binaries for user-billing and ai-orchestrator; they
reuse the embedded migration packages and exit after `Up` completes. Existing
SQL has not been proven backward compatible as a permanent policy.

`current.env`, `previous.env`, and `in-progress.env` contain only SHA, UTC
timestamps, status, backup path, migration/smoke status, and image digests.
Files are written with mode `0600` using temporary-file-plus-rename. No env
dump, password, webhook secret, token, or key content is stored.

## Recovering an interrupted deployment

`in-progress.env` is deliberately retained after failure or interruption and a
new deployment refuses to overwrite it. Do not delete it merely to make the
next command run. As the deployment operator:

1. acquire or verify that no process holds `state/deploy.lock`;
2. inspect only the non-secret fields in `in-progress.env` and compare its SHA,
   `status`, `migration_status`, `backup_path`, and image digests with
   `current.env` and `previous.env`;
3. verify the referenced backup and checksum exist and match;
4. inspect Compose container state, restart counts, migration logs, and schema
   migration ledgers;
5. if migrations started, follow the manual-intervention policy and do not run
   old images unless compatibility was reviewed;
6. copy `in-progress.env` into the incident record, then remove only that stale
   file explicitly (`rm -- /opt/snaphost/state/in-progress.env`) after the
   runtime/database state and recovery plan are known.

There is intentionally no force/continue flag and no automatic stale-state
deletion.

If PostgreSQL has never started and there is no current state, deployment is
treated as bootstrap and records that no pre-existing database was available
to back up. If current state exists but PostgreSQL is unavailable, deployment
stops instead of pretending a backup succeeded.

After either migration job starts, automatic rollback is forbidden unless the
operator explicitly sets `MIGRATIONS_BACKWARD_COMPATIBLE=true`. Otherwise a
failure records `manual-intervention-required`, prints the previous SHA and
backup path, and never restores the database or starts old images automatically.
The temporary curl config containing `WEBHOOK_SECRET` is mode `0600` and is
removed by a dedicated cleanup trap on success, error, `INT`, `TERM`, and normal
process exit.
See [rollback](rollback.md) and [backup and restore](backups.md).

## Relationship to staging

Nothing enforces a staging deployment before a production one. The production
workflow validates that the SHA is exactly 40 hex characters, that it is an
ancestor of `origin/main`, and that its images exist in GHCR — it does not ask
whether that SHA ever ran anywhere else. Promotion is a practice, not a gate.

Staging is currently suspended and its deploy job is gated behind
`STAGING_DEPLOY_ENABLED` (see [staging deployment](staging-deployment.md)), so
today a commit reaches production without a rehearsal. The database backup
taken before migrations and the manual-intervention hold after them are what
stand in for it; they limit damage rather than prevent it.

## Frontend

The dashboard is built and deployed from the separate
[`justaba/snaphost-ui`](https://github.com/justaba/snaphost-ui) repository. Its
manual production workflow accepts an exact frontend SHA from its own `main`,
builds with production `VITE_*` values, uploads a retained artifact, and then
atomically switches `/opt/snaphost/frontend/current`. A failed HTTPS smoke check
restores the previous frontend symlink.

The frontend workflow authenticates as `frontend-deployer`, which owns only
`/opt/snaphost/frontend/` and has no sudo, Docker membership, or access to
backend env, secrets, releases, state, or backups. The backend `deployer`
identity keeps ownership of the rest of `/opt/snaphost`.

Backend and frontend Git histories are independent. Record both SHAs for every
production state; never assume that a backend SHA identifies the browser
bundle. Deploy additive backend compatibility first, then the frontend, and
remove the old API shape only after the previous frontend is no longer a
rollback target.

Required backend configuration in this repository's `production` GitHub
Environment:

| Kind | Name | Value |
| --- | --- | --- |
| variable | `DEPLOY_SSH_USER` | `deployer` |
| variable | `DEPLOY_SSH_PORT` | `22` |
| variable | `DEPLOY_ROOT` | `/opt/snaphost` |
| variable | `DEPLOY_COMPOSE_PROJECT` | `snaphost` |
| variable | `DEPLOY_GHCR_USERNAME` | GHCR account used for image pulls |
| variable | `DEPLOY_SMOKE_URL` | `https://api.snaphost.ru` (base URL; the scripts append their own paths) |
| secret | `DEPLOY_SSH_HOST` | production host address |
| secret | `DEPLOY_SSH_KNOWN_HOSTS` | pinned host key line; update whenever the host is rebuilt |
| secret | `DEPLOY_SSH_PRIVATE_KEY` | deployer key, never a root key |

## GitHub production entrypoint

Production deployment is only available through the manual
`.github/workflows/production-deploy.yml` workflow. Its `sha` input must be
exactly 40 hexadecimal characters, all six backend manifests must already exist
in GHCR, and the SHA must be an ancestor of `origin/main`. Branch/PR-only commits
are rejected before SSH. The deployment job is gated by the `production` GitHub Environment.
Configure required reviewers there when the repository plan supports them;
repository YAML cannot enforce reviewer policy by itself. The current private
repository plan did not expose Environment protection rules on 2026-08-12, so
manual dispatch and exact-SHA validation are the active fallback controls.

The production Environment uses the same secret/variable names documented in
[staging deployment](staging-deployment.md), with production-only values and a
separate VDS. Production and staging must not share env files, keys, state,
backups, Docker volumes, domains, databases, or Yandex resources. Both invoke
the same release helper, `deploy.sh`, and production Compose manifest. No
Terraform apply or automatic database restore is part of CD.
