# ADR 0008 — Remove the service-to-service HTTP API

Status: Accepted
Date: 2026-09-02
Updated: 2026-09-04

## Context

Task 1 merged seven services into one process and replaced every cross-service
HTTP client with a direct call through `internal/wiring`. What it did not do was
remove the endpoints those clients had called, or the clients themselves.

What was left behind compiled, passed tests, and had no callers:

- the `/internal/*` route group, authenticated by `shared.WebhookAuth` and
  `WEBHOOK_SECRET`, carrying deploy status updates, running-state persistence,
  the image-deleted marker, expired-deploy and pending-image listings, deploy
  reads, host route lookup, API-key verification and TLS authorization;
- `runtime/billing.Client` — six methods against `user-billing`, a service
  deleted with billing in Task 1, in a package still named for it;
- `saga.HTTPBuilderClient` and `saga.HTTPRunnerClient`, never constructed;
- `builder/ai.HTTPClient`, constructed only by its own test;
- the handler packages `ai/api`, `builder/api` and `runtime/api`.

Two of those had drifted far enough to be visibly wrong.
`POST /api/v1/ai/generate-dockerfile` was registered after Casbin with no policy
line, so it answered `403` to every role including `admin`, while the policy
file carried a line for `/api/v1/ai/status/:id` — a handler that has never
existed here. Neither could be reached; nothing noticed, because
`rbac_policy_test.go` asserts named absences and never compares the policy
against the routes actually registered.

The cost was never runtime overhead. It was a mutating HTTP surface guarded by a
single shared secret that nothing in the tree presented, and a set of clients
that describe an architecture this repository no longer has. Both invite reuse:
the next caller needing a status write finds `POST /internal/deploys/:id/status`
before it finds the repository method, and gets a network boundary, a secret and
a serialisation step for a call between two goroutines.

## Decision

Components communicate through typed Go interfaces in one process, and that is
the only mechanism.

- Delete the `/internal/*` group, `shared.WebhookAuth` and `WEBHOOK_SECRET`.
- Delete the unused HTTP clients and the `ai/api`, `builder/api` and
  `runtime/api` handler packages. The narrow contracts that survive are Go
  interfaces next to the code that needs them: `saga.BuildScheduler`,
  `saga.Runtime`, `runtime/deployments`, `builder/scheduler`.
- Rename `gateway` to `httpapi`. The package is a middleware chain in front of
  this binary's own handlers, not a service in front of other services, and the
  old name is the reason the misregistered AI route looked plausible.
- Replace transport-shaped error signalling with `saga.OperationError`, which
  carries retryability rather than an HTTP status. A permanent runtime refusal
  is a Go error the saga compensates, not a `422` it has to interpret.
- `/internal/*` stays unrouted rather than becoming a namespace for something
  else, so a package boundary cannot quietly become a privileged network
  boundary again.

## Consequences

- **The TLS authorization handler went with the group, and that was a real
  loss.** `GET /internal/tls/authorize` answered correctly; what it never had
  was a way for standard Caddy `ask` to authenticate to it, which is why
  [ADR 0007](0007-custom-domain-tls-edge.md) already recorded the edge as
  incomplete. Task 4 built its replacement from nothing rather than adapting a
  handler whose authentication was wrong for its only intended caller. The new
  `/tls/ask` handler is the only route on a dedicated listener, fails closed and
  returns no domain data.
- **The generic host route lookup is gone.** Local generated hostnames resolve
  from Docker labels. The production replacement is a dedicated reverse-proxy
  listener that consumes the durable alias model directly and exposes no route
  lookup response. [ADR 0006](0006-project-deployment-alias.md) records the
  split.
- `WEBHOOK_SECRET` disappears from both env examples, both Compose files and
  `deploy.sh preflight`. A variable left in an installed `.env` is inert.
- `deploy.sh` loses the two smoke checks that exercised `/internal/routes` with
  a valid and an invalid secret; the deployment suite goes from 39 tests to 36.
  Readiness, the restart-loop check and the public health smoke are unchanged.
- The configuration selectors that only described alternatives that do not
  exist go too: `RUNNER_BACKEND`, amended into
  [ADR 0004](0004-provider-boundary.md), and `ALIAS_IDLE_GC_DAYS`, whose sweep
  is removed in ADR 0006.
- **Reversing this is deliberately expensive.** Putting a component back on the
  far side of HTTP now requires an authentication design rather than an
  environment variable, which is the correct price for a boundary that is
  root-equivalent on this host.

## Alternatives

Keeping the group behind its secret "for a future edge" is what produced this
ADR: the endpoints had already outlived their callers by one whole task, and the
edge that was supposed to use them cannot present the header they require.
Unused code does not stay correct — two of these routes had drifted into being
unreachable, and the drift was invisible precisely because nothing called them.
