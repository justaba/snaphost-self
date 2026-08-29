# snaphost-self

Self-hosted deployment platform for one operator. Point it at a repository or a
local folder, get a running site with a domain, a database, and authentication —
on the cheapest VPS tier a provider sells.

Working name. Forked from [SnapHost](https://github.com/justaba/snaphost), a
multi-tenant hosting SaaS, on 2026-08-29.

## Status

**Mid-refactor. Not installable yet.**

The fork inherited a working build and runtime pipeline and is being reduced to
one binary — see [Task 1](docs/tasks/active/0001-collapse-to-one-binary.md) for
what is done and what is not. Until it lands, the tree still starts as the
inherited multi-service stack.

## Why not Dokploy or Coolify

Both want ~2 GB before they host anything, because both carry a language runtime
and a framework plus PostgreSQL, Redis, and a reverse proxy. This targets under
170 MB idle by being a single static Go binary with an embedded store, so the
memory goes to the sites instead of the panel.

What it keeps from the fork and they do not have: a build pipeline that clones or
unpacks an untrusted project, generates a Dockerfile with an LLM when the repo
has none, and refuses to report a deploy healthy until something actually answers
on the injected port.

## Requirements

- Docker 24+
- Go 1.25+ (to build)

## Project structure

```
snaphost-self/
├── snaphost-backend/      one Go module
│   ├── cmd/               entry points — nine today, one after Task 1
│   ├── internal/
│   │   ├── gateway/       JWT auth, RBAC, proxying
│   │   ├── control/       deploys, projects, domains, saga orchestration
│   │   ├── builder/       clone/unpack → Dockerfile → BuildKit → image
│   │   ├── runtime/       starts containers on Docker, probes them, enforces TTL
│   │   ├── ai/            generates a Dockerfile when the repo has none
│   │   └── shared/        webhook auth and Dockerfile validation
│   └── docker/            one Dockerfile per image
├── infra/                 Compose manifests, Caddy, deploy and backup scripts
└── docs/
    ├── architecture/      how the system works
    ├── decisions/         ADRs worth keeping from upstream
    ├── operations/        runbooks
    ├── tasks/             current work
    └── inherited/         the SaaS task catalog this forked from
```

Nine entry points under `cmd/` is the shape being removed, not the target: the
whole point of [Task 1](docs/tasks/active/0001-collapse-to-one-binary.md) is
one process. They are separate for now so the collapse can be done and
verified one seam at a time.

## Commands

```
make dev-backend    bring the stack up locally
make test           go test ./... in every module
make lint           golangci-lint in every module (needs v1.64.x)
make logs           tail compose logs
```

## Relationship to upstream

`git remote` is deliberately empty: this repository must never push to SnapHost.
History is preserved because `git blame` on the build and runtime paths still
explains real production incidents — the `PORT` liveness probe, the registry
authentication boundary, the Dockerfile cache schema bump.

Upstream fixes are not automatically relevant here. The two products diverge on
their first premise: SnapHost runs other people's code and charges for it; this
runs the operator's own and charges nobody.
