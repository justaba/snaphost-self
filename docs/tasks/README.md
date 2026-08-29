# Task catalog

Status: Current
Updated: 2026-08-07

## Active

| Task | Status | Document |
| --- | --- | --- |
| 11 — Reproducible production deployment | In progress — production live and monitored; rollback rehearsal, off-host restore drill, and the approval-gate decision remain | [active/0011-production-deployment.md](active/0011-production-deployment.md) |
| 12 — Separate staging/production SA keys | Implemented — preflight guard verified on production; Lockbox sourcing (item 4) deferred | [active/0012-credential-separation.md](active/0012-credential-separation.md) |
| 13 — Observability split (admin logs vs user feedback) | 13a implemented but blocked: Yandex refuses the log group (PermissionDenied); collection off in production. 13b planned | [active/0013-observability-and-user-feedback.md](active/0013-observability-and-user-feedback.md) |
| 14 — Vibecoder ingress (editor extension, API keys, multi-source) | In progress | [active/0014-vibecoder-ingress.md](active/0014-vibecoder-ingress.md) |
| 15 — Runtime port contract (user-Dockerfile validation, liveness probe) | Implemented — staging proof and the Task 13b hand-off (item 6) remain | [active/0015-runtime-port-contract.md](active/0015-runtime-port-contract.md) |
| 16 — Custom domains for user deploys | 16a/16b/16c/16d/16f/16g done and proven end to end on a real domain 2026-08-05; auto-promotion and the project-key server side landed 2026-08-06, awaiting release. The MCP client must send a project_key (Task 14e) | [active/0016-custom-domains.md](active/0016-custom-domains.md) |
| 17 — Operator console | 17a (read surface) implemented 2026-08-07 and verified against a real PostgreSQL. Actions with an audit trail (17b) and metrics (17c) remain | [active/0017-admin-console.md](active/0017-admin-console.md) |

## Partially completed

| Task | Remaining work |
| --- | --- |
| 10.7 — Yandex registry integration | Run one automated UI-to-BuildKit-to-Yandex end-to-end proof. |

## Completed milestones

- [Tasks 0–8: backend security and pipeline refactor](completed/0000-0008-backend-refactor.md)
- [Tasks 9–9.4: project detection and Dockerfile policy](completed/0009-project-detection.md)
- [Tasks 10.0–10.10: Yandex runtime adapter](completed/0010-yandex-runtime.md)
- [CI modernization](completed/ci-modernization.md)

## Backlog

See [backlog.md](backlog.md). Backlog items are not active work until they are
moved into `active/` with scope and acceptance criteria.

## Historical evidence

The [archive](archive/README.md) contains original prompts, implementation
notes, smoke results, and superseded assumptions. It is retained for audits and
debugging, but is not the current architecture specification.

## Status rules

- **Planned** — scope exists; implementation has not started.
- **In progress** — implementation is underway.
- **Partially completed** — useful behavior exists, but bounded work or proof remains.
- **Completed** — implementation and proportionate verification are recorded.
- **Archived** — historical context, not a current source of truth.
