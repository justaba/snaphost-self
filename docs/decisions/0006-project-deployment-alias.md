# ADR 0006 — Project, deployment, and alias

Status: Accepted
Date: 2026-07-29

## Context

A deploy owned both halves of what a published site needs: it was the build
result *and* the address people visit. That works while an address is
disposable, and stops working the moment a user attaches a domain of their own.

`deploys.subdomain` is unique per deploy and every deploy gets a fresh one, so
there was nothing a domain could be bound to that survives a redeploy. Deploys
also expire: `CONTAINER_DEFAULT_TTL_MIN` was 30 minutes and the watchdog tore
them down. Binding a domain to a `deploy_id` would mean the site goes dark half
an hour later and has to be re-pointed on every push.

Task 16 needed a stable publish target before anything else in it could be
built.

## Decision

Split the two roles into three layers, matching what Vercel and Netlify settled
on:

| Layer | Table | Lifetime | Mutable | Role |
| --- | --- | --- | --- | --- |
| Project | `projects` | permanent | yes | owner-scoped identity; owns attached domains |
| Deployment | `deploys` | long, GC-able | no | one build, permanently addressable at its own subdomain |
| Alias | `custom_domains` | permanent | yes (pointer) | a hostname pointing at exactly one deployment |

A domain is a **pointer**, not a property of a deployment. Publishing means
building a new immutable deployment and moving the pointer at it; rollback
means moving the pointer back, with no rebuild and no new image. A superseded
deployment stays reachable at its own URL, which is what makes the pointer move
safe: the previous deployment answers every request until the update commits.

Deploys of the same source converge on one project through a deterministic
source key (`git:<host>/<path>#<branch>`). An uploaded archive carries no stable
identity across deploys, so each upload gets its own project rather than
silently joining an unrelated one.

## Consequences

### The TTL becomes garbage collection, not a deadline

The 30-minute TTL was sized for the Docker backend, where a container holds host
RAM for as long as it exists. On Serverless Containers an idle deployment scales
to zero and costs approximately nothing; the standing cost is its registry
image. So the watchdog's authority changes shape. `GET /internal/deploys/expired`
now returns three kinds of deploy and never one an alias points at:

1. un-aliased deploys past `ttl_expires_at` — the pre-16 behavior, unchanged;
2. deploys whose alias the idle sweep released after `ALIAS_IDLE_GC_DAYS`
   without a single request;
3. deploys beyond `PROJECT_DEPLOY_RETENTION` newest per project.

Inactivity replaces the timer for pinned deployments because a hard timer cannot
express "live but quiet", which is the normal state of a small published site.
Traffic is recorded in `deploys.last_request_at` at route-lookup time, throttled
to at most one write every few minutes — the idle window is measured in days, so
minute resolution is far more than the decision needs.

The watchdog itself did not change: all three cases arrive through the endpoint
it already calls.

### TTL becomes a tier value, enforced where the tier is known

`runner-svc` applies any non-zero `ttl_minutes` it receives with no upper bound.
The ceiling therefore lives in `user-billing` (`DEPLOY_TTL_MIN`,
`DEPLOY_TTL_MAX_MIN`), with the entitlement rather than with the executor. The
endpoint is internal-only, so this is correctness for the tier model, not a
public attack surface.

### Route resolution grows a second branch

Hosts under `${DOMAIN_SUFFIX}` still resolve through `deploys.subdomain`; any
other host resolves through a `verified` row in `custom_domains`. Both branches
end on the same condition — `running` with a non-empty `container_id`. An
unverified, revoked, or unknown host is a `404`, indistinguishable from a dead
subdomain, which is what keeps a hostname someone else controls from being
served.

### What this costs

- Two more tables and a backfill on `deploys`; rows predating the change were
  grouped by source, and archive deploys each got their own project.
- One write per deploy per few minutes on the request path, best-effort: route
  lookup must never fail because the bookkeeping write did.
- Projects have no fields of their own yet, so there is no
  `GET /api/v1/projects`; the dashboard groups deploys client-side. That is
  honest today and will not be once a project owns a name or settings.

### What this unlocks beyond custom domains

Pointer-based rollback, zero-downtime redeploys, and stable per-project URLs all
require exactly this model. The custom domain is one consumer of it, not its
only justification — which is why 16a was worth landing before the TLS question
(ADR pending, Task 16c) was answered.

## Related

- [Task 16](../tasks/active/0016-custom-domains.md) — scope and remaining work.
- [Deploy lifecycle](../architecture/deploy-lifecycle.md) — the flow this
  changes, including what the watchdog may reap.
- ADR 0003 — the central router that performs the host lookup this ADR splits
  in two.
