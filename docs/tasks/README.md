# Task catalog

Status: Current
Updated: 2026-08-30

## Active

No active task is recorded. Task 1 is complete; Task 7 is the next fully scoped
task but remains blocked on its version and registry decision.

## Completed

| Task | Result | Document |
| --- | --- | --- |
| 1 — Collapse the control plane into one binary | One Go application process with SQLite, local identity, direct package wiring, in-process queues, local Docker images and an embedded React panel. Current documentation was rewritten after the code settled. | [completed/0001-collapse-to-one-binary.md](completed/0001-collapse-to-one-binary.md) |

The last recorded comparable footprint was 44.7 MiB across four containers
before the registry was removed; the application was 6.8 MiB. The current local
manifest has three containers and has not been remeasured as a complete stack.
Real build pressure on a 1 GB host is also still unmeasured.

## Planned

| Task | Scope | Document |
| --- | --- | --- |
| 2 — Deploys that live forever | Per-project environment variables, persistent volumes and TTL as an opt-in for previews rather than the default. | — |
| 3 — Managed services | Template-defined databases and application authentication resources with volumes, health checks and injected connection data. | — |
| 4 — The edge | Integrate Caddy with verified-domain authorization and dynamic Docker routing; remove Traefik from the intended production path. | — |
| 5 — Git webhooks | Deploy on push. | — |
| 6 — Operator actions | Restart, stop and audited shell access to a container. | — |
| 7 — Install and upgrade without us | A third party installs, upgrades and rolls back on their own host without repository-owner SSH or GitHub environments. | [planned/0007-install-and-upgrade.md](planned/0007-install-and-upgrade.md) |

Tasks 2, 3 and 6 include their corresponding panel work. Task 1 delivered the
first operator UI rather than a final interface for future resources.

## Status rules

- Planned — scope exists and implementation has not started.
- In progress — implementation is underway.
- Partially completed — useful behavior exists, but bounded work or proof
  remains.
- Completed — scoped implementation and proportionate verification are
  recorded, with any transferred follow-up named explicitly.
