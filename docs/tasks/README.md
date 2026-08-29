# Task catalog

Status: Current
Updated: 2026-08-29

## Active

| Task | Status | Document |
| --- | --- | --- |
| 1 — Collapse the control plane into one binary | In progress — the cloud runtime path, dead docs, billing and the module split are gone, the platform is one process, and the store is SQLite. Removing Supabase, the in-process queue, `GOMEMLIMIT`, the docs rewrite and the embedded panel remain | [active/0001-collapse-to-one-binary.md](active/0001-collapse-to-one-binary.md) |

Remaining order inside Task 1: **6a** remove Supabase and issue operator
identity → **7** in-process queue → **8** `GOMEMLIMIT` → **9** docs → **10** the
panel. 10 sits last because it logs into 6a.

Measured after item 6: **56.1 MiB idle across five containers**, from 131.1 MiB
across twelve. The application itself is 9.6 of that.

## Planned

These are scoped only as far as the ordering argument in Task 1. None has an
acceptance criteria section yet, and none starts before Task 1 lands.

| Task | Scope |
| --- | --- |
| 2 — Deploys that live forever | Per-project environment variables, persistent volumes, TTL as an opt-in for previews rather than the default. Without this the platform hosts previews, not sites. |
| 3 — Managed services | Databases **and** authentication as one subsystem: image + volume + env + health check + a connection string injected into the app. Postgres, Redis, PocketBase from templates, so a fourth is a file rather than code. |
| 4 — The edge | Caddy terminating TLS for every attached domain, driven by the control binary. The inherited on-demand issuance and TXT verification carry over. |
| 5 — Git webhooks | Deploy on push. |
| 6 — Operator actions | Restart, stop, and a shell into a container. Needs the audit trail the inherited console deliberately waited for. |

Each of tasks 2, 3 and 6 carries its own screens. Task 1 item 10 is the panel's
first pass — the inherited pages adapted to an operator login and a product
with no billing — not the last word on it.

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
