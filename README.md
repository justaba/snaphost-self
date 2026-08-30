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

The single-binary architecture is implemented and tested. The repository is
usable for development, but it is **not yet a generally installable self-hosted
product**: the production manifest and SSH deployment workflow still describe
the original operator's hosts. Installation, upgrades and a supported release
contract are [Task 7](docs/tasks/planned/0007-install-and-upgrade.md).

Current limitations that matter operationally:

- deployed sites are previews with a TTL; long-lived environments and volumes
  are future work;
- Caddy is the chosen production edge, but the Docker/Caddy integration and
  installation flow are not implemented yet; local development uses Traefik;
- Docker images are not reclaimed when a deploy is removed or expires;
- managed databases and application authentication are not implemented.

## Architecture

The application is one Go process with an embedded React panel and SQLite
store. Redis, PostgreSQL, the local registry, Supabase, Terraform and the cloud
runtime were removed.

```text
browser / API client
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
and Traefik. The production manifest has `snaphost` and `buildkitd`; its edge is
currently a host prerequisite rather than part of the product.

See [the architecture overview](docs/architecture/overview.md) and
[deployment lifecycle](docs/architecture/deploy-lifecycle.md) for the detailed
contract.

## Requirements

- Docker 24+ with Compose v2
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
│   │   ├── gateway/           HTTP middleware, RBAC and WebSocket logs
│   │   ├── control/           auth, projects, deploys, domains and saga
│   │   ├── builder/           clone/unpack, detection, build and scan
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

## Relationship to upstream

This repository is a separate product and has no configured upstream push
remote. Upstream-specific documentation is not shipped in this tree; repository
history remains available through Git when old context is needed.
