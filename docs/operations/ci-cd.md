# CI and deployment

Status: Current
Type: Operations
Updated: 2026-08-30

## CI contract

.github/workflows/pipeline.yml runs on pull requests and pushes to main.

The Go job, from the single snaphost-backend module, runs:

- go test ./...;
- go vet ./...;
- golangci-lint v1.64.8 built with the job's Go toolchain.

The shell job checks syntax and ShellCheck diagnostics for deployment, backup
and uptime scripts, runs the fake-command deployment and backup suites, and
verifies the systemd backup unit syntax.

After those jobs pass, one Docker image is built. On main it is published as:

~~~text
ghcr.io/<owner>/<repository>/snaphost:<40-character-git-sha>
~~~

The Docker build compiles the React panel and embeds it in the binary. CI does
not currently run the panel's Vitest, ESLint or Prettier commands separately;
a successful asset build is therefore weaker than the local panel verification
contract.

The old .github/workflows/ci.yml is an inert manual tombstone.

## Staging and production workflows

The optional staging job verifies and deploys the exact image SHA over SSH only
when STAGING_DEPLOY_ENABLED is true. It is currently disabled because there is
no staging host.

.github/workflows/production-deploy.yml is a manual exact-SHA deployment. It
requires the requested commit to be an ancestor of main, verifies the image,
uses a pre-verified SSH host key and delegates to
.github/scripts/deploy-remote.sh.

Both workflows are deployments for the repository owner's existing machines.
They do not provision a host, install Docker or Caddy, create DNS, generate
production secrets, configure backups or define a stable public release
channel. They must not be presented as a third-party installer. Task 7 owns
that replacement.

## Production manifest

infra/docker-compose.prod.yml runs:

- snaphost from the exact GHCR SHA;
- snaphost-migrate as a profile-only one-shot using the same SQLite volume;
- rootless buildkitd.

The manifest publishes only the application port. TLS, firewall policy and the
routing edge are host prerequisites. It contains no PostgreSQL, Redis, registry
or Traefik service.

The production env example is exhaustive for this environment, not a promise
that all of those variables belong in the future installer.

## Release behavior

infra/deploy.sh provides preflight, deploy and rollback operations. A deploy:

1. validates the SHA, environment and rendered Compose;
2. takes an exclusive lock;
3. records in-progress state;
4. creates and verifies a transactional SQLite dump;
5. pulls the exact application image;
6. runs the one-shot migrator;
7. updates snaphost and checks readiness and public smoke;
8. records current and previous release state atomically.

Migration rollback is never assumed. Once migrations begin, application
rollback requires MIGRATIONS_BACKWARD_COMPATIBLE=true after an operator reviews
the schema delta. Database restore is always a separate manual action.

See [rollback](rollback.md) and [backup and restore](backups.md).

## Repository settings

The current workflows require:

- GitHub Actions package write permission;
- environment-scoped SSH host, verified known_hosts and private-key secrets;
- the matching deployment user, root path, Compose project and smoke URL;
- a GHCR token already installed on the target host.

Production Environment reviewers and branch protection are external repository
settings. Workflow checks do not create them.
