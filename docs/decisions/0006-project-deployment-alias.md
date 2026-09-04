# ADR 0006 — Project, deploy and alias

Status: Accepted
Date: 2026-07-29
Updated: 2026-09-04

## Context

A deploy originally represented both a build result and the address people
visited. That fails once an address must survive a redeploy: each deploy has a
new image, container, generated hostname and expiry.

## Decision

Split publishing into three layers:

| Layer | Lifetime | Mutable state | Role |
| --- | --- | --- | --- |
| Project | permanent | metadata and future settings | Stable owner-scoped identity for one source. |
| Deploy | one build result | lifecycle status only | Image, container, logs, generated hostname and expiry for one build. |
| Custom domain alias | permanent until revoked | target deploy | Verified hostname pointing to one running deploy in the project. |

A domain belongs to a project and points to a deploy. Publishing a successful
build moves the pointer after the runtime probe passes. Rollback moves it to an
older running deploy. Neither operation rebuilds an image.

Git sources converge through a deterministic repository and branch key.
Uploaded archives have no natural identity, so clients must send a stable
project_key to converge; otherwise each upload creates a new project.

## Consequences

### TTL and retention

The current Docker runtime uses TTLs because every live container consumes host
resources. The watchdog may stop an expired or excess deploy, but never one
currently selected by a verified alias.

PROJECT_DEPLOY_RETENTION adds project-aware reclamation.
Task 2 will make TTL opt-in for long-lived sites and add persistent project
configuration; this ADR does not by itself make deploys permanent.

### Route lookup

Generated hosts resolve from Docker labels in local development. Production
generated and custom hosts use the dedicated edge contract described by ADR
0007. The old central-router lookup endpoint remains deleted; the replacement
is a narrow Host proxy on a separate listener rather than an internal API.

### Consistency

Alias promotion is one SQLite update, so a domain never points at no deploy
between versions. Promotion is best-effort after deploy success: a failure
keeps serving the previous target and does not tear down the new working
container.

### Cost

The model adds project and domain tables, source-key rules, retention queries
and one mutable pointer. In return it makes publishing and rollback independent
of building and preserves a stable identity for future environment variables,
volumes and managed services.

## Related

- [Deploy lifecycle](../architecture/deploy-lifecycle.md)
- [Deployment model](../architecture/deployment-model.md)
- [Custom domains](../operations/custom-domains.md)
