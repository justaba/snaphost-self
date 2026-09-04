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

The shell job checks syntax and ShellCheck diagnostics for deployment, backup,
operator and uptime scripts; runs the 53-scenario deployment, 41-scenario
backup and 21-scenario install/upgrade suites; and verifies the systemd backup
unit syntax.

After those jobs pass, one Docker image is built. On main it is published as:

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

The Docker build compiles the React panel and embeds it in the binary. CI does
not currently run the panel's Vitest, ESLint or Prettier commands separately;
a successful asset build is therefore weaker than the local panel verification
contract.

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
- rootless buildkitd.

The manifest publishes only the application port. TLS, firewall policy and the
routing edge are host prerequisites. It contains no PostgreSQL, Redis, registry
or Traefik service. The application has an in-container `/health` healthcheck;
the deploy script waits for Docker to report `healthy` and does not accept a
merely running process as ready.

The production env example requires three operator decisions and documents
optional overrides. Application defaults remain in Go rather than being copied
into a second configuration surface.

## Release behavior

infra/deploy.sh provides preflight, deploy and rollback operations. A deploy:

1. validates a strict `vMAJOR.MINOR.PATCH`, the environment and rendered Compose;
2. takes an exclusive lock;
3. records in-progress state;
4. pulls the exact application image and records its digest;
5. creates and verifies a transactional SQLite dump;
6. runs the one-shot migrator;
7. updates snaphost and waits for its Docker healthcheck;
8. runs the optional public smoke check;
9. pins the successful version through atomic replacements of `production.env`
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
