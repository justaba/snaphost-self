# Task catalog

Status: Current
Updated: 2026-08-29

## Active

| Task | Status | Document |
| --- | --- | --- |
| 1 — Collapse the control plane into one binary | In progress — the cloud runtime path, dead docs, billing and the module split are gone, the platform is one process, the store is SQLite, and identity is issued here rather than by Supabase. The in-process queue, `GOMEMLIMIT`, the docs rewrite and the embedded panel remain | [active/0001-collapse-to-one-binary.md](active/0001-collapse-to-one-binary.md) |

Remaining order inside Task 1: **7** in-process queue → **8** `GOMEMLIMIT` →
**10** the panel → **9** docs. Then Task 7.

Two of those are dependencies. Item 7 moves a 50 MB upload out of RAM and adds
an in-process queue, so a `GOMEMLIMIT` picked before it is a number to re-tune
after. Item 9 describes what is there, and 7 and 10 both change what that is —
so the docs go last, and `CLAUDE.md` stays wrong for one more item while
carrying its banner about it.

Item 10 moved ahead of 9 on 2026-08-29. It had been last because it logs into
item 6a, and 6a is done.

Measured after item 6a, before anyone logs in: **48.1 MiB idle across five
containers**, from 131.1 MiB across twelve. The application itself is 7.4 of
that — and 28.8 after a login, which is heap Go keeps rather than anything
argon2 is still holding. Bounding it is item 8.

## Planned

None starts before Task 1 lands. Tasks 2 to 6 are scoped only as far as the
ordering argument in Task 1 and have no documents yet; task 7 has one, because
its scope came out of finding two live defects in the deployment path rather
than out of planning.

| Task | Scope | Document |
| --- | --- | --- |
| 2 — Deploys that live forever | Per-project environment variables, persistent volumes, TTL as an opt-in for previews rather than the default. Without this the platform hosts previews, not sites. | — |
| 3 — Managed services | Databases **and** authentication as one subsystem: image + volume + env + health check + a connection string injected into the app. Postgres, Redis, PocketBase from templates, so a fourth is a file rather than code. | — |
| 4 — The edge | Caddy terminating TLS for every attached domain, driven by the control binary. The inherited on-demand issuance and TXT verification carry over. | — |
| 5 — Git webhooks | Deploy on push. | — |
| 6 — Operator actions | Restart, stop, and a shell into a container. Needs the audit trail the inherited console deliberately waited for. | — |
| 7 — Install and upgrade without us | Someone who is not us installs, upgrades and rolls back on their own machine. Today CI deploys one specific host over SSH, which is why the rollback path could not run for several commits without anyone noticing. Blocked on the version and registry model. | [planned/0007-install-and-upgrade.md](planned/0007-install-and-upgrade.md) |

Each of tasks 2, 3 and 6 carries its own screens. Task 1 item 10 is the panel's
first pass — the inherited pages adapted to an operator login and a product
with no billing — not the last word on it.

**Numbering.** These are task numbers. The 7, 8, 9 and 10 that appear beside
Task 1 are items inside its work plan, not tasks — which is why the next free
task number is 7 and not 11.

## Historical

[../inherited/](../inherited/) holds the task catalog of the SaaS this was
forked from. It explains why much of the kept code is shaped the way it is, and
it is not a description of this product.

## Status rules

Inherited from the upstream project, because they were doing real work there:

- **Planned** — scope exists; implementation has not started.
- **In progress** — implementation is underway.
- **Partially completed** — useful behavior exists, but bounded work or proof remains.
- **Completed** — implementation and proportionate verification are recorded.
