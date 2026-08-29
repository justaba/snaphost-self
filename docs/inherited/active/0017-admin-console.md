# Task 17 — Operator console

**Status:** 17a (read surface) implemented 2026-08-07, together with the
`billing/topup` fix below. 17b (actions with an audit trail) and 17c (metrics)
not started.
**Created:** 2026-08-07
**Updated:** 2026-08-07

## Problem

There is no operator view of the platform. Every question an operator can have
— who is this account, why did their deploy fail, where did their coins go,
which domains are stuck — is answered today by opening a `psql` session on the
production host. That is the same gap [Task 13a](0013-observability-and-user-feedback.md)
found from the other side: diagnosis needs a shell.

It is also a prerequisite for taking real money. A payment integration creates
obligations ("I paid and the coins never arrived") that cannot be serviced
without a way to see and reconcile an account. Building payments first would
mean building this anyway, under the pressure of live transactions.

## What shipped in 17a

Read-only. Nothing in this pass mutates a user's state — see the 17b note.

### The missing identity (migration 0012)

The control plane had no record of who a `user_id` belongs to. `wallets.user_id`
was the only identity, and the Supabase user webhook received an `email` and
**discarded it** — so no operator screen could name an account without querying
a different database.

- `users` (`id`, `email`, `created_at`, `updated_at`), seeded by the existing
  `POST /internal/users` webhook, which now persists what it is given.
- The upsert never overwrites a stored email with an empty one: a redelivered
  webhook carrying less information must not lose an address.
- Backfilled from the union of `wallets`, `deploys`, and `transactions`, not
  from `wallets` alone. An account whose seed webhook never fired has no wallet
  but may own deploys — and that is exactly the account an operator goes
  looking for.

Emails collected before this migration are gone; they were never stored and
cannot be reconstructed here.

### API — `GET /api/v1/admin/*` (user-billing)

`overview`, `users`, `users/:id`, `users/:id/{deploys,transactions,domains,projects,keys}`,
`deploys`, `deploys/:id`, `transactions`, `domains`. All paginated with an exact
total, `limit` defaulting to 50 and capped at 200.

`GET /admin/deploys/:id` is the screen worth naming: it returns the deploy, the
**saga row** that produced it (step, flags, retry count, last error), the ledger
entries it moved, and the domains published on it. Those four together answer
almost every "why is this deploy like that" question that previously needed a
database session.

### Authorisation — two independent checks

1. api-gateway's Casbin policy admits `/api/v1/admin/*` for the `admin` role
   only, enforced on the real URL path as every other route is.
2. user-billing re-checks the forwarded `X-User-Role` before answering.

The second is not redundant. The gateway's policy lives in a CSV that a new
route can be added without, and this service would then serve every account's
data to any authenticated user. Authorisation for operator data should not
depend on remembering to edit a separate file.

`X-User-Role` is trustworthy for the same reason `X-User-ID` already is: the
gateway's `Enrich` middleware overwrites it from the verified token and deletes
it when the request is unauthenticated, and user-billing publishes no host port.

**The role itself comes from the `snaphost_role` JWT claim, which is written by
a Supabase Postgres hook that is configured by hand** ([production-deployment.md](../../operations/production-deployment.md)).
If that hook is not enabled on the project, every token falls back to `user` and
the console is inaccessible to everyone, including its owner. That is the first
thing to check if the pages 403.

### What is deliberately not selectable

- `api_keys.key_hash` — the only stored credential in this database. A test
  asserts the serialised key response contains no `hash`/`secret`/`token`
  substring, so widening `APIKeyRow` by copying from `apikey.Repository` fails
  loudly.
- `custom_domains.verification_token` — it proves nothing to an operator and is
  the one value that lets someone else pass the ownership check.
- Database errors, which can carry query text and column names, never reach a
  response body; they go to the service log.

### Dashboard

`/dashboard/admin`, gated by `requiredRole="admin"` — a convenience only, since
the gateway refuses the data regardless of what the browser renders. Tabs:
сводка, пользователи, деплои, транзакции, домены, plus per-account and
per-deploy detail pages.

The overview leads with a KPI row of stat tiles rather than charts: these are
single current values, and the number is the chart. One tile reports a
condition rather than a quantity — accounts with no wallet — and it carries an
icon and words as well as color, with a panel naming the cause (the Supabase
seed webhook) so the finding is actionable rather than decorative.

### Verification

- Handler tests: the role gate (six refusal cases including `administrator` and
  `admin,user`, four casings of the accepted value), pagination clamping,
  filter parsing, a malformed `user_id` degrading to "no filter" rather than a
  400, 404 mapping, database-error redaction, `items: []` never serialising as
  `null`, and the key-hash assertion above.
- **Repository tests against a real PostgreSQL** — the only thing that catches a
  wrong column name, a scan order drifted from its SELECT list, or a join that
  silently drops rows. Gated on `ADMIN_TEST_DATABASE_URL`; the file's doc
  comment carries the exact container and migration commands. All twelve
  migrations plus the 0012 down/re-up cycle were applied and the six tests
  passed on `postgres:16` on 2026-08-07.

## Not done, and why

### 17b — operator actions

Every mutation an operator would want — stop or delete a deploy, adjust a
balance, refund, seed a missing wallet, revoke a key or a domain, ban an account
— needs an **audit trail before it needs a button**: who did it, when, to whom,
and what the value was before. That is a table and a policy decision, not a
handler, and shipping the buttons first would mean production mutations with no
record of who made them.

The primitives all exist already (`/internal/billing/{reserve,commit,refund}`,
deploy delete, domain revoke, key revoke), so 17b is mostly the audit table plus
a confirmation UX.

### 17c — metrics

Every service exposes `/metrics` and nothing scrapes it
([monitoring.md](../../operations/monitoring.md)). Saga failure rate, wallet
reserve/commit mismatch, and container restart counts are the three worth
alerting on. Out of scope here; it belongs with Task 13.

## The `topup` hole — closed 2026-08-07

`POST /api/v1/billing/topup` was a **public, `user`-role endpoint that minted
vibecoins from a request body with no payment verification**. Any authenticated
account, or any `sk_` key, could grant itself an unlimited balance and therefore
unlimited build and runtime resources. Nothing verified that money had changed
hands, because nothing collects money yet.

Found while scoping this task, deferred by the owner until 17a landed, fixed
immediately after:

- the Casbin policy line is gone, so the gateway refuses the path for every
  role;
- the handler moved into the `/internal` group behind `X-Webhook-Secret` and
  now takes an explicit `user_id` — the caller credits somebody else's wallet,
  so the account can never be implied from a forwarded header;
- `routes/routes_test.go` asserts the public route is not registered at all
  (404, not 401), that the internal one refuses four flavours of missing or
  wrong secret, and that it rejects a body with no user, no idempotency key, or
  a non-positive amount;
- the gateway's `routes/rbac_policy_test.go` reads the shipped policy file and
  fails if any role — including `admin` — can reach the path again.

Nothing called it: the dashboard had only a `TODO` where a top-up button would
go, and the MCP server never used it. So there was no client to migrate.

This is also the seam the payment integration lands on. `Repository.Topup` is
idempotent on `idempotency_key`, so the provider's payment id goes there and a
redelivered callback credits exactly once.

## Acceptance criteria

- An operator can find an account by email or UUID and see its balance,
  reserved funds, deploys, ledger, projects, domains, and keys. **Met.**
- A failed deploy's cause is visible without a database session: status,
  failure reason, saga step and flags, and the coin movements it produced.
  **Met.**
- A non-admin receives `403` from both the gateway and the service. **Met** in
  tests; not yet exercised against a deployed environment.
- No credential or verification token is reachable through the console. **Met**,
  with a test that fails if the shape widens.
- Every operator action is recorded with actor, target, and prior value.
  **Not applicable to 17a** — no actions exist yet. It is the gate on 17b.

## Related

- [Task 13](0013-observability-and-user-feedback.md) — the operator/user split
  this console sits on the operator side of.
- [Task 11](0011-production-deployment.md) — the Supabase webhook whose absence
  the "accounts without a wallet" tile reports.
- [security.md](../../architecture/security.md) — the redaction rule the error
  handling here follows.
