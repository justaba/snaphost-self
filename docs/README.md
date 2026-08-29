# SnapHost documentation

Status: Current
Updated: 2026-07-30

This is the single entry point for project documentation.

## I want to understand the system

- [Architecture overview](architecture/overview.md)
- [Services and responsibilities](architecture/services.md)
- [Deployment lifecycle](architecture/deploy-lifecycle.md)
- [Deployment model](architecture/deployment-model.md)
- [Security model](architecture/security.md)
- [Yandex runtime architecture](architecture/yandex-runtime.md)

## I want to run or operate SnapHost

- [Local development](operations/local-development.md)
- [CI/CD](operations/ci-cd.md)
- [Yandex Cloud setup](operations/yandex-cloud-setup.md)
- [Yandex runtime configuration](operations/yandex-runtime.md)
- [Public address contract](operations/public-address.md) — the address customer DNS points at
- [Production deployment](operations/production-deployment.md)
- [Staging deployment contract](operations/staging-deployment.md)
- [Staging infrastructure provisioning](operations/staging-provisioning.md)
- [Backup and restore](operations/backups.md)
- [Credentials](operations/credentials.md) — which keys belong to which environment
- [Custom domains](operations/custom-domains.md) — the customer-domain edge
- [Monitoring](operations/monitoring.md)
- [Rollback](operations/rollback.md)
- [Troubleshooting](operations/troubleshooting.md)

## I want to understand why a decision was made

- [Architecture decision records](decisions/README.md)

## I want to see project work and status

- [Task catalog](tasks/README.md)
- [Active Task 11: reproducible production deployment](tasks/active/0011-production-deployment.md)
- [Backlog](tasks/backlog.md)
- [Historical archive](tasks/archive/README.md)

## Document types

- `architecture/` describes how the system works now.
- `operations/` contains procedures and runtime configuration.
- `decisions/` explains important choices and tradeoffs.
- `tasks/` tracks active work, completed milestones, backlog, and history.

Historical task logs are evidence, not current architecture. When history and
an active architecture or operations document disagree, the active document is
the source of truth and the inconsistency should be fixed.
