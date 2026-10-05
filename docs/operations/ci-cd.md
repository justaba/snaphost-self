# CI and deployment

Status: Current
Type: Operations
Updated: 2026-09-04

## CI contract

.github/workflows/pipeline.yml runs on pull requests, pushes to main and strict
`vMAJOR.MINOR.PATCH` tags.

The Go job, from the single snaphost-backend module, runs:

- go test ./...;
- go vet ./...;
- golangci-lint v1.64.8 built with the job's Go toolchain.

The panel job uses Node.js 22 and the exact pnpm version declared in
`web/package.json`. It installs the frozen lockfile, then runs Vitest, ESLint,
Prettier and the production Vite build as separate steps.

The shell job checks syntax and ShellCheck diagnostics for deployment, backup
and operator scripts; runs the deployment, backup and install/upgrade suites;
and verifies the systemd backup unit syntax. It also renders the real
production Compose file, validates the stock digest-pinned Caddy 2.10.2
Caddyfile, and runs local on-demand TLS and persistent-state integration checks.

After the Go, panel and shell jobs pass, one Docker image is built. On main it
is published as:

~~~text
ghcr.io/<owner>/<repository>/snaphost:<40-character-git-sha>
~~~

A strict semantic-version tag publishes both that SHA tag and the operator
release tag:

~~~text
ghcr.io/<owner>/<repository>/snaphost:v1.2.3
~~~

No `latest` tag is published. Pull requests build the image but publish
nothing.

The Docker build compiles the React panel again and embeds it in the binary.
The dedicated panel job remains the frontend verification boundary: a broken
test, lint rule, format check or standalone production build prevents image
publishing.

The old .github/workflows/ci.yml is an inert manual tombstone.

## Publishing is not deployment

CI no longer connects to staging or production over SSH. The former staging
job, manual production workflow and remote-deploy scripts were deleted as the
first implementation step of Task 7. Publishing a version only makes its image
available; an operator's host pulls and deploys it locally.

First install and checkout-aware upgrade now run on the operator's host through
`infra/snaphostctl`; CI only tests that command and publishes the image. The
fake-command coverage is not a real-host acceptance rehearsal, so Task 7 stays
open and image publishing alone must not be presented as proof of an operable
third-party installation.

## Production manifest

infra/docker-compose.prod.yml runs:

- snaphost from an exact GHCR semantic-version tag;
- snaphost-migrate as a profile-only one-shot using the same SQLite volume;
- rootless buildkitd;
- stock Caddy 2.10.2 pinned by image digest.

The manifest publishes the application recovery port on loopback. Caddy alone
publishes 80/443 and sends routing and certificate-authorization requests to
internal application listeners over the control network; it has no Docker
socket. The manifest contains no PostgreSQL, Redis, registry or Traefik
service. The application has an in-container `/health`
healthcheck; the deploy script waits for Docker to report `healthy` and does
not accept a merely running process as ready.

The production env example requires three operator values and documents
optional overrides. Application defaults remain in Go rather than being copied
into a second configuration surface.

## Release behavior

infra/deploy.sh provides preflight, deploy and rollback operations. A deploy:

1. validates a strict `vMAJOR.MINOR.PATCH`, the environment and rendered Compose;
2. takes an exclusive lock;
3. records in-progress state;
4. pulls the exact application image and records its digest;
5. creates and verifies a transactional SQLite dump;
6. recreates BuildKit so bind-mounted daemon configuration from the release is
   applied, then waits for its healthcheck;
7. runs the one-shot migrator;
8. updates snaphost and waits for its Docker healthcheck;
9. runs the optional public smoke check;
10. pins the successful version through atomic replacements of `production.env`
   and the current/previous state files.

SHA arguments and `latest` are refused. For the first transition only, rollback
can read and preserve a legacy `sha=` state written by the old deploy script;
new successful deploys record `version=`.
Before deploy or rollback changes containers, it also refuses an env version
that disagrees with `current.env`; an interrupted multi-file update is visible
and requires reconciliation rather than being silently compounded.

Migration rollback is never assumed. Once migrations begin, application
rollback requires MIGRATIONS_BACKWARD_COMPATIBLE=true after an operator reviews
the schema delta. Database restore is always a separate manual action.

See [install and upgrade](install-and-upgrade.md), [rollback](rollback.md) and
[backup and restore](backups.md).

## Repository settings

The publishing job requires GitHub Actions package write permission. The GHCR
package must also be made public in repository/package settings; the workflow
cannot assert visibility. No deployment SSH key, GitHub environment or GHCR
credential on an operator host belongs to the intended installation contract.
