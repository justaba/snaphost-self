# Architecture decision records

Status: Current
Updated: 2026-07-29

ADRs explain decisions that are expensive to reverse or easy to misunderstand.
They do not replace current architecture documents.

| ADR | Status | Decision |
| --- | --- | --- |
| [0002](0002-scan-after-push.md) | Superseded | Scan registry images after push. Kept for its reasoning; this fork has no registry and runs vulnerability scanning behind a flag, since the code being scanned is the operator's own. |
| [0004](0004-provider-boundary.md) | Accepted | Keep runtime-specific behavior behind provider interfaces. Vindicated by the fork: removing an entire cloud runtime was three files and two switch statements. |
| [0006](0006-project-deployment-alias.md) | Accepted | Split publishing into project, immutable deployment, and alias; TTL becomes GC that never reaps an aliased deployment. |
| [0007](0007-custom-domain-tls-edge.md) | Accepted | Terminate custom-domain TLS at our own Caddy edge with gated on-demand issuance. Upstream chose this over a cloud gateway; here there is no other option, and the implementation carries over unchanged. |

Removed with the cloud runtime path: 0001 (Yandex SDK version), 0003 (central
router), and 0005 (Terraform state backends). They decided questions this
product does not have.

Historical experiments and smoke evidence remain in
[`../inherited/archive/implementation-log.md`](../inherited/archive/implementation-log.md).
