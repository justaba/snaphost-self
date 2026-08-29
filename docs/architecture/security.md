# Security model

Status: Current
Type: Architecture
Updated: 2026-08-03

SnapHost executes untrusted repository content, so validation is layered.

- Clone URLs use host allowlists, private/metadata IP filtering, DNS pinning,
  and TLS hostname verification.
- Work directories use canonical path containment checks.
- Generated and user Dockerfiles use separate configured image policies. Both
  reject missing tags and `latest`.
- Image references are normalized before policy checks.
- Runner verifies registry path, deploy-ID tag, stored ownership, and state.
- Delete verifies the stored `container_id`; arbitrary client cloud IDs are not
  trusted.
- Runtime resources receive ownership labels where supported.
- Builder, runner, router, and Terraform credentials are separated by role.
- Provider errors are redacted before entering user-visible logs.
- The public control plane exposes exactly `GET /internal/routes` for the
  Yandex router. It uses constant-time `X-Webhook-Secret` validation before
  proxying to billing; no wildcard `/internal/*` proxy exists.

## Isolation between tenants in the browser

Deploys serve untrusted code from unrelated users, so browser-side boundaries
matter as much as runtime ones.

- The control plane lives on its own registrable domain, so no deployed
  application can set a cookie the dashboard or API will receive. This is the
  reason for the [domain split](deployment-model.md), not a naming preference.
- `localStorage`, `sessionStorage`, and IndexedDB are keyed by origin, so two
  deploys never reach each other's storage.
- Cookies are keyed by registrable domain, so deploys under the shared suffix
  *can* reach each other's. The durable fix is a Public Suffix List entry for
  the deploy domain, which makes each deploy hostname its own site. Until it
  lands, `router-svc` strips the `Domain` attribute from deploy responses, which
  covers server-set cookies but not ones written by page JavaScript. Treat the
  gap as open until the PSL entry is merged.

Production must keep PostgreSQL, Redis, and BuildKit off public interfaces.
Production also keeps user-billing unexposed. Router route lookup must use TLS;
Lockbox supplies the shared secret, while firewall rules and rate limiting are
additional controls rather than replacements for authentication.
Container-level metadata-network egress blocking remains backlog
defense-in-depth.
