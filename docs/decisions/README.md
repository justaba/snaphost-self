# Architecture decision records

Status: Current
Updated: 2026-08-30

ADRs explain choices that are expensive to reverse. Their status is explicit
because several decisions came from the upstream SaaS and no longer describe
this fork.

| ADR | Status | Current decision |
| --- | --- | --- |
| [0002](0002-scan-after-push.md) | Superseded | There is no registry. BuildKit loads the image into the local Docker daemon, then Trivy scans it there and removes it on a gated critical result. |
| [0004](0004-provider-boundary.md) | Accepted, reduced | Runtime lifecycle stays behind an interface, but Docker is the only implementation and no registry provider boundary remains. |
| [0006](0006-project-deployment-alias.md) | Accepted | A permanent project owns immutable build results; a custom domain is a mutable pointer to one running deploy. |
| [0007](0007-custom-domain-tls-edge.md) | Accepted, incomplete | Custom-domain TLS belongs at an operator-owned Caddy edge with fail-closed verification. The post-collapse Docker routing and Caddy ask integration are not implemented. |

Removed ADRs concerned Yandex SDK versions, a central cloud router and
Terraform state. They are not current product decisions; old context is
available only through Git history.
