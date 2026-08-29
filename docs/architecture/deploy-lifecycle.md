# Deploy lifecycle

Status: Current
Type: Architecture
Updated: 2026-07-03

1. The authenticated user creates a deploy through `user-billing`.
2. Billing reserves coins and records the deploy.
3. The saga submits a build job and waits for a Redis build event.
4. Builder securely clones, detects, validates, builds, and scans the project.
5. The successful event carries image reference, commit SHA, and port.
6. Runner verifies registry prefix, deploy tag, ownership, image reference,
   and deploy state before starting a backend.
7. Docker creates a local container, or Yandex deploys a Serverless Container.
8. Billing records runtime ID, endpoint, subdomain, TTL, and running state.
9. Delete or TTL expiry verifies the stored mapping before removing resources.

The reasoning behind this split is recorded in
[ADR 0006](../decisions/0006-project-deployment-alias.md).

Since Task 16a a deploy also belongs to a **project**: a permanent,
owner-scoped publish target resolved from the deploy's source. A deploy stays
immutable and addressable at its own subdomain; a **custom domain** is an alias
row pointing at one deploy of the project. Publishing and rollback are the same
operation — moving that pointer — and neither rebuilds anything.

This changes what the watchdog may reap. `GET /internal/deploys/expired` now
returns three kinds of deploy, and never one an alias points at:

1. un-aliased deploys past `ttl_expires_at` (unchanged behavior),
2. deploys whose alias was unpinned by the idle sweep after `ALIAS_IDLE_GC_DAYS`
   without a single request — a hard timer cannot express "live but quiet",
3. deploys beyond `PROJECT_DEPLOY_RETENTION` newest per project.

Route resolution has two branches that converge on the same condition. Hosts
under `${DOMAIN_SUFFIX}` resolve through `deploys.subdomain` as before; any
other host resolves through a `verified` row in `custom_domains`. Unknown,
unverified, and revoked hosts are `404`.

Two state machines exist:

- `deploys.status` is user-facing: `pending`, `reserved`, `building`,
  `provisioning`, `running`, and terminal states.
- `deploys.current_step` tracks saga internals and includes `built`.

Build completion is a Redis event. It is not a user-facing `status='built'`.
