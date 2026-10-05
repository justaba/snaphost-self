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

The single-binary architecture and operator installation path are implemented
and tested. Public releases `v0.1.0` and `v0.1.1` passed a real install,
checkout-aware upgrade, rollback and encrypted local SQLite restore; see
[completed Task 7](docs/tasks/completed/0007-install-and-upgrade.md) and the
[install and upgrade runbook](docs/operations/install-and-upgrade.md).
Public DNS/ACME acceptance is recorded in
[completed Task 4](docs/tasks/completed/0004-production-edge.md).

This remains an **experimental preview platform**, with the limitations below.
Published application images currently target amd64.

Current limitations that matter operationally:

- deployed sites are previews with a TTL; long-lived environments and volumes
  are future work;
- Compose now installs Caddy and the application-side routing/TLS authorization
  adapters. Production projects use verified individual domains with
  fail-closed on-demand TLS; public DNS/ACME proof passed. Local development
  uses Traefik;
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

- Linux amd64, 2 vCPU and 1 GiB RAM for the measured small React/Vite workload;
  size RAM for actual builds and running sites
- 20 GiB disk recommended; installation/upgrade require at least 5 GiB free
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

AI Dockerfile generation is optional and disabled by default. Projects use
their own Dockerfile or a built-in template. To enable the provider fallback,
set `LLM_ENABLED=true` and `OPENROUTER_API_KEY` in the protected env file.
A key left in an existing env file does not enable AI by itself. Without AI,
projects with no matching template ask the operator to add a Dockerfile.

On the first start, open the private setup link and choose your own login and
password. The account is created once; later visits show the regular login
form. See [operator setup](docs/operations/operator-setup.md) for the local
command that prints the link. No password is generated or written to logs.

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
not invent a `latest` tag or deploy from `main`. The current experimental
release is [v0.1.2](https://github.com/justaba/snaphost-self/releases/tag/v0.1.2).
The operator command is:

```text
snaphostctl install <vMAJOR.MINOR.PATCH>
snaphostctl upgrade [vMAJOR.MINOR.PATCH]
snaphostctl rollback [--dry-run]
snaphostctl setup-link
```

The installer pulls a digest-pinned stock Caddy image and persists certificate
state below `/opt/snaphost/state/caddy`. The operator points the panel hostname
and each verified project domain at the VPS; see the
[custom-domain runbook](docs/operations/custom-domains.md).

## Relationship to upstream

This repository is a separate product and has no configured upstream push
remote. Upstream-specific documentation is not shipped in this tree; repository
history remains available through Git when old context is needed.
