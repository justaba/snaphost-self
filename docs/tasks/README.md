# Task catalog

Status: Current
Updated: 2026-10-05

## Active

Task 7 is in progress. Its version/registry model, CI separation,
first-install command and checkout-aware upgrade/rollback implementation are
done. A
[partial VPS rehearsal](../operations/rehearsals/2026-09-04-vps.md) covers the
real install, login, local restore and 4 GB build-pressure paths. The
[isolated 1 GiB arm64 drill](../operations/rehearsals/2026-10-05-task7-1g.md)
adds a React/Vite build and encrypted scheduled SQLite restore. Install,
upgrade and rollback against public GitHub/GHCR artifacts remain.

Completed Task 4 provides the Compose-managed edge: Caddy exclusively owns
80/443 and calls an internal data-plane proxy plus a separate fail-closed
`ask` listener without a Docker socket. Installer/upgrade/rollback lifecycle
and persistent state are wired. The public DNS/ACME rehearsal passed for both
control and project domains, including local encrypted TLS restore. The
supported release-install path remains Task 7's acceptance work. See
[the completed task](completed/0004-production-edge.md).

Six pieces of work landed outside the numbered tasks. Four closed recorded
gaps, one is the smallest useful part of Task 6, and one removed structure that
Task 1 left behind rather than adding anything:

- **Image reclamation.** The watchdog releases the local Docker image of any
  deploy that reaches a terminal status, and deploys.image_deleted_at records
  it. This was the top entry under "known gaps" in every architecture document
  and is no longer one.
- **Build cache reclamation.** Both standalone BuildKit configurations enable
  automatic OCI-worker GC. The policy protects 512 MB of warm cache, starts
  broader reclamation above 4 GB, and reacts below 5 GB of host free space.
  Rollout and rollback recreate BuildKit so a changed bind-mounted policy is
  applied immediately. This is intentionally separate from deploy-image GC.
- **Frontend verification in CI.** A dedicated Node.js 22 job installs the
  panel's frozen pnpm lockfile and runs Vitest, ESLint, Prettier and the
  production build. Image publishing depends on it, so the embedded panel can
  no longer bypass its own verification contract.
- **Operator actions on the dashboard, and no admin console.** The whole
  /api/v1/admin surface and its seven panel screens are deleted. They existed
  so one role could administer other people's accounts; a self-hosted install
  has one account, so the dashboard is the console. What replaced them:

  - GET /api/v1/projects and DELETE /api/v1/projects/:id on the ordinary user
    surface, the deletion writing an admin_audit_log row in the same
    transaction as the removal;
  - POST /api/v1/deploys/:id/stop and .../start, which are now different
    operations. Stopping keeps the image so starting is a container run rather
    than a rebuild; deleting is what releases the disk. The panel previously
    had one button wired to DELETE and labelled «Остановить», so pausing a site
    destroyed it;
  - the saga state that only an admin screen used to show, folded into the
    deploy detail — it is the only answer to "why is this stuck";
  - GET /api/v1/audit and a screen for it under Настройки, so the audit log is
    read by something other than sqlite3.

  Two dead entries went on the way: the Транзакции tab, pointing at a route
  deleted with billing in Task 1, and a «Перезапустить» button calling an
  endpoint this backend has never had.

- **The embedded vulnerability scanner is gone**, with the memory it reserved
  in the application cgroup and the CLI it needed in the runtime image. This
  fork trusts the source its own operator submits; an operator with a different
  trust boundary scans in their own CI. See
  [ADR 0003](../decisions/0003-remove-embedded-scanning.md).
- **The microservice remains are gone.** Task 1 replaced every cross-service
  HTTP call with a direct one but left the far ends standing: the
  `/internal/*` group and its webhook secret, three unused HTTP clients, the
  `runtime/billing` package, the `ai`/`builder`/`runtime` API handler packages,
  and configuration selectors for backends that do not exist. Two routes had
  already drifted into being unreachable without anything noticing, which is
  the argument. `gateway` is `httpapi` now, because it is a middleware chain
  and not a service. See
  [ADR 0008](../decisions/0008-no-service-to-service-http.md).

Task 6 is therefore partially completed rather than planned. Stop, start,
delete and the audit read surface are done; audited shell access into a
container remains.

## Completed

| Task | Result | Document |
| --- | --- | --- |
| 1 — Collapse the control plane into one binary | One Go application process with SQLite, local identity, direct package wiring, in-process queues, local Docker images and an embedded React panel. Current documentation was rewritten after the code settled. | [completed/0001-collapse-to-one-binary.md](completed/0001-collapse-to-one-binary.md) |
| 4 — Production edge | Pinned Caddy, fail-closed project domains and dynamic routing passed local and public staging/production ACME checks, alias lifecycle, and encrypted local TLS-state restoration. Published-release installation remains Task 7. | [completed/0004-production-edge.md](completed/0004-production-edge.md) |

The last recorded comparable footprint was 44.7 MiB across four containers
before the registry was removed; the application was 6.8 MiB. The current local
manifest has three containers and has not been remeasured as a complete stack.
The 2026-09-04 cold Node build peaked at 997.2 MiB of host memory while another
site served, but it ran on a 4 GB machine. The 2026-10-05 isolated 1 GiB arm64
host built a small React/Vite application in 9.7 seconds without swap, at
564.9 MiB peak host RAM, while the control plane and an existing site served.
It used a local registry; published CI images currently target amd64.

## Planned

| Task | Scope | Document |
| --- | --- | --- |
| 2 — Deploys that live forever | Per-project environment variables, persistent volumes and TTL as an opt-in for previews rather than the default. | — |
| 3 — Managed services | Template-defined databases and application authentication resources with volumes, health checks and injected connection data. | — |
| 5 — Git webhooks | Deploy on push. | — |
| 6 — Operator actions *(partially completed)* | Stop, start, project deletion and the audit read surface are done. Audited shell access into a container remains. | — |
| 7 — Install and upgrade without us *(in progress)* | A third party installs, upgrades and rolls back on their own host without repository-owner SSH or GitHub environments. Code, runbook, isolated 1 GiB Node build and encrypted local restore passed; public-release install/upgrade/rollback remain. | [planned/0007-install-and-upgrade.md](planned/0007-install-and-upgrade.md) |

Tasks 2, 3 and 6 include their corresponding panel work. Task 1 delivered the
first operator UI rather than a final interface for future resources.

## Status rules

- Planned — scope exists and implementation has not started.
- In progress — implementation is underway.
- Partially completed — useful behavior exists, but bounded work or proof
  remains.
- Completed — scoped implementation and proportionate verification are
  recorded, with any transferred follow-up named explicitly.
