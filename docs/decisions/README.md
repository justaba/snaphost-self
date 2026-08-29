# Architecture decision records

Status: Current
Updated: 2026-07-29

ADRs explain decisions that are expensive to reverse or easy to misunderstand.
They do not replace current architecture documents.

| ADR | Status | Decision |
| --- | --- | --- |
| [0001](0001-yandex-sdk-v0.md) | Accepted | Use Yandex Go SDK v0.31 for the current adapter. |
| [0002](0002-scan-after-push.md) | Accepted | Scan registry images after push and delete vulnerable local-registry images. |
| [0003](0003-central-router.md) | Accepted | Use a central router for concurrent Yandex hostname routing. |
| [0004](0004-provider-boundary.md) | Accepted | Keep cloud-specific behavior behind provider interfaces and build tags. |
| [0005](0005-terraform-state-backends.md) | Accepted | Isolate state per provider/environment and use the fixed Yandex HTTPS endpoint as its TLS control. |
| [0006](0006-project-deployment-alias.md) | Accepted | Split publishing into project, immutable deployment, and alias; TTL becomes GC that never reaps an aliased deployment. |
| [0007](0007-custom-domain-tls-edge.md) | Accepted | Terminate customer-domain TLS at our own Caddy edge with gated on-demand issuance, not on the Yandex gateway. |

Historical experiments and smoke evidence remain in
[`tasks/archive/implementation-log.md`](../tasks/archive/implementation-log.md).
