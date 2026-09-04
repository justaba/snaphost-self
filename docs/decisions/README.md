# Architecture decision records

Status: Current
Updated: 2026-09-02

ADRs explain choices that are expensive to reverse. Their status is explicit
because several decisions came from the upstream SaaS and no longer describe
this fork.

| ADR | Status | Current decision |
| --- | --- | --- |
| [0002](0002-scan-after-push.md) | Superseded | Registry and local-image scanning decisions were removed with the single-host trust model. |
| [0003](0003-remove-embedded-scanning.md) | Accepted | No vulnerability scanner is bundled or run; operators with an untrusted-code boundary scan externally. |
| [0004](0004-provider-boundary.md) | Accepted, reduced | Runtime lifecycle stays behind an interface, but Docker is the only implementation, no registry provider boundary remains, and configuration no longer offers a backend selector. |
| [0006](0006-project-deployment-alias.md) | Accepted | A permanent project owns immutable build results; a custom domain is a mutable pointer to one running deploy. |
| [0007](0007-custom-domain-tls-edge.md) | Accepted, implementation in progress | Compose Caddy exclusively owns 80/443 and uses internal proxy and fail-closed `ask` listeners without a Docker socket. Generated-host certificate policy and real DNS/ACME proof remain. |
| [0008](0008-no-service-to-service-http.md) | Accepted | Components talk through Go interfaces only. The `/internal/*` group, the webhook secret and every unused HTTP client are deleted, and `/internal/*` stays unrouted. |

Removed ADRs concerned Yandex SDK versions, a central cloud router and
Terraform state. They are not current product decisions; old context is
available only through Git history.
