# snaphost-self

Self-hosted deployment platform for one operator. Give it a Git repository or
an uploaded archive and it builds a container image, starts the application on
the same Docker host, verifies that it answers on the injected port, and exposes
it through the operator panel.

The project was forked from [SnapHost](https://github.com/justaba/snaphost), a
multi-tenant hosting SaaS, on 2026-08-29. The SaaS control plane has since been
collapsed into one Go process; the completed work and its measurements are in
[Task 1](docs/tasks/completed/0001-collapse-to-one-binary.md).

## Status

The single-binary architecture is implemented and tested. A versioned
first-install, checkout-aware upgrade and coordinated rollback command now
exist, with the operator procedure in the
[install and upgrade runbook](docs/operations/install-and-upgrade.md). The
project is still **not production-ready**: the install path has only a partial
VPS rehearsal, and public DNS/ACME acceptance is not done. The remaining work is tracked in
[Task 4](docs/tasks/planned/0004-production-edge.md) and
[Task 7](docs/tasks/planned/0007-install-and-upgrade.md).

Current limitations that matter operationally:

- deployed sites are previews with a TTL; long-lived environments and volumes
  are future work;
- Compose now installs Caddy and the application-side routing/TLS authorization
  adapters, but real public DNS/ACME proof and the generated-host certificate
  strategy remain; local development uses Traefik;
- managed databases and application authentication are not implemented.

## Architecture

The application is one Go process with an embedded React panel and SQLite
store. Redis, PostgreSQL, the local registry, Supabase, Terraform and the cloud
runtime were removed.

```text
browser / API client
        |
        v
       Caddy
        |
        v
  snaphost binary
  + auth and RBAC
  + projects, deploys, domains and saga
  + build pipeline and AI Dockerfile generation
  + Docker runtime and watchdog
  + embedded React panel
        |                 |
        v                 v
    BuildKit         Docker daemon
                          |
                          v
                    user containers
```

The local Compose topology has three services: `snaphost`, rootless `buildkitd`
and Traefik. The production manifest has `snaphost`, rootless `buildkitd` and a
pinned Caddy service. Caddy alone publishes 80/443 and consumes two internal
listeners for dynamic routing and certificate authorization without receiving
the Docker socket.

The standalone BuildKit cache is automatically garbage-collected in both
topologies. Its configured targets retain at least 512 MB of warm cache, start
broader reclamation above 4 GB, and react when host free space falls below
5 GB. These are periodic GC targets, not a hard quota during an active build.

See [the architecture overview](docs/architecture/overview.md) and
[deployment lifecycle](docs/architecture/deploy-lifecycle.md) for the detailed
contract.

## Requirements

- Docker 24+ with Compose v2
- systemd, AppArmor and Git for a production install (the supplied profile
  targets Ubuntu 24.04)
- Go version from `snaphost-backend/go.mod` for local Go builds
- Node.js and pnpm 11 only when developing the panel outside the image build

## Project structure

```text
snaphost-self/
├── snaphost-backend/
│   ├── cmd/
│   │   ├── snaphost/          application entry point
│   │   └── control-migrate/   one-shot SQLite migrator
│   ├── internal/
│   │   ├── httpapi/           HTTP middleware, RBAC and WebSocket logs
│   │   ├── control/           auth, projects, deploys, domains and saga
│   │   ├── builder/           clone/unpack, detection, validation and build
│   │   ├── runtime/           Docker lifecycle, probe and watchdog
│   │   ├── ai/                templates and LLM fallback
│   │   ├── panel/             embedded panel assets
│   │   └── wiring/            direct adapters between packages
│   ├── web/                   React/Vite operator panel
│   └── docker/                application image
├── infra/                     Compose, deployment and backup tooling
└── docs/                      architecture, operations, decisions and tasks
```

## Local development

```bash
cp infra/.env.example infra/.env
make dev
make logs-svc SVC=snaphost
```

On the first start, the application creates the operator account and writes a
generated one-time password to the `snaphost` container log. Open
`http://localhost:8080` and sign in with `OPERATOR_EMAIL` (by default
`operator@localhost`) and that password.

For panel hot reload, keep the stack running and start:

```bash
make dev-panel
```

Then open `http://localhost:5173`. Vite proxies `/api` and `/ws` to the Go
process.

Useful commands:

```text
make dev                 app + BuildKit + Traefik
make dev-backend         app + BuildKit
make build               build the application image and embedded panel
make test                Go tests
make lint                Go lint
make test-panel          Vitest suite
make lint-panel          ESLint
make logs                follow Compose logs
make clean               remove containers, networks and local volumes
```

`make clean` deletes the local SQLite and BuildKit volumes.

## Production installation

The supported layout is a release-tag checkout at `/opt/snaphost`. Start with
the [install and upgrade runbook](docs/operations/install-and-upgrade.md); do
not invent a `latest` tag or deploy from `main`. The operator command is:

```text
snaphostctl install <vMAJOR.MINOR.PATCH>
snaphostctl upgrade [vMAJOR.MINOR.PATCH]
snaphostctl rollback [--dry-run]
```

The installer starts the pinned Compose Caddy service and persists its state
below `/opt/snaphost/state/caddy`. The operator must point the panel hostname,
generated-host wildcard and custom-domain traffic at the VPS; see the
[custom-domain runbook](docs/operations/custom-domains.md).

## Relationship to upstream

This repository is a separate product and has no configured upstream push
remote. Upstream-specific documentation is not shipped in this tree; repository
history remains available through Git when old context is needed.
