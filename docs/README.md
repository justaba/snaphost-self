# Documentation

Status: Current
Updated: 2026-08-29

Single entry point for project documentation.

## I want to understand the system

- [Architecture overview](architecture/overview.md)
- [Services and responsibilities](architecture/services.md)
- [Deployment lifecycle](architecture/deploy-lifecycle.md)
- [Deployment model](architecture/deployment-model.md)
- [Security model](architecture/security.md)

**These describe the inherited multi-service architecture and are stale by
design until [Task 1](tasks/active/0001-collapse-to-one-binary.md) lands.** They
are accurate about how the code works today and wrong about where it is going;
rewriting them is item 9 of that task, deliberately last, because rewriting a
description of something mid-demolition wastes the work twice.

## I want to run or operate it

- [Local development](operations/local-development.md)
- [CI/CD](operations/ci-cd.md)
- [Custom domains](operations/custom-domains.md)
- [Backup and restore](operations/backups.md)
- [Monitoring](operations/monitoring.md)
- [Rollback](operations/rollback.md)
- [Credentials](operations/credentials.md)
- [Troubleshooting](operations/troubleshooting.md)

Several of these still describe deploying the upstream SaaS to its own VDS. A
self-hosted product installs on the operator's machine instead, so the
deployment and credential runbooks need rewriting rather than editing.

## I want to understand why a decision was made

- [Architecture decision records](decisions/README.md)

## I want to see project work and status

- [Task catalog](tasks/README.md)
- [Task 1: collapse the control plane into one binary](tasks/active/0001-collapse-to-one-binary.md)

## History

- [Inherited task catalog](inherited/README.md) — the SaaS this forked from.
  Evidence for why the kept code is shaped the way it is; not a description of
  this product.

## Document types

- `architecture/` describes how the system works now.
- `operations/` contains procedures and runtime configuration.
- `decisions/` explains important choices and tradeoffs.
- `tasks/` tracks active work.
- `inherited/` is upstream history, retained for debugging and blame.

When a historical document and an active one disagree, the active one is the
source of truth and the inconsistency should be fixed.
