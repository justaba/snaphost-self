# Task 16 — Custom domains for user deploys

**Status:** 16a, 16b, 16c, 16d, 16f and the 16g runbook are done. A customer
domain was taken end to end on production on 2026-08-05: attach → TXT
verification → on-demand certificate → the deploy answering on
`https://test.kinocassa.ru`. Automatic alias promotion (item 4) and the
server side of a stable project identity for uploads landed 2026-08-06 and are
awaiting a release.

Remaining: the identity gate (item 18), the 13b feedback hook (item 32), and
the client half of the project key — the MCP server must send one for a domain
to follow successive agent deploys ([Task 14](0014-vibecoder-ingress.md)).
**Created:** 2026-07-25
**Updated:** 2026-08-06

## Implementation status (2026-07-26)

Shipped in this pass:

- **16a** — `projects` table, `deploys.project_id`, `deploys.last_request_at`
  ([0010_projects.up.sql](../../../snaphost-backend/internal/control/db/migrations/0010_projects.up.sql),
  [internal/project](../../../snaphost-backend/internal/control/project/project.go)).
  Deploys of the same source converge on one project through a deterministic
  source key; archive uploads get their own project, having no stable identity.
  Alias-pinned deploys are excluded from TTL expiry, idle aliases are unpinned
  after `ALIAS_IDLE_GC_DAYS` without traffic, and superseded deploys beyond
  `PROJECT_DEPLOY_RETENTION` are reclaimed — all through the existing
  `GET /internal/deploys/expired` sweep, so the watchdog needed no change.
  Per-deploy TTL is now sent by the saga (`DEPLOY_TTL_MIN`) and capped in
  user-billing (`DEPLOY_TTL_MAX_MIN`); production examples ship 1440.
- **16b** — `custom_domains`
  ([0011_custom_domains.up.sql](../../../snaphost-backend/internal/control/db/migrations/0011_custom_domains.up.sql)),
  public API `POST/GET /api/v1/domains`, `DELETE /api/v1/domains/:id`, and
  `POST /api/v1/domains/:id/target` (the pointer move that serves both publish
  and rollback), with matching `rbac_policy.csv` lines. Attach validates the
  hostname, refuses our own suffix, enforces `MAX_DOMAINS_PER_USER`, and is
  rate-limited per user in Redis. A background verifier resolves the TXT
  challenge and re-checks verified domains on `DOMAIN_REVERIFY_HOURS`.
- **16f** — Domains page in the dashboard
  ([DomainsPage.tsx](../../../snaphost-ui/src/pages/dashboard/DomainsPage.tsx)):
  attach form scoped to a project, copyable TXT/CNAME/A records, status that
  polls only while a domain can still change on its own, detach with
  confirmation, and a repoint dialog listing the project's running deploys
  with their own permanent URLs and the current target marked. A project page
  ([ProjectPage.tsx](../../../snaphost-ui/src/pages/dashboard/ProjectPage.tsx),
  `/dashboard/projects/:projectId`) shows the project's domains and its full
  build history, with "make active" on any running deploy — rollback without
  going through the domains page. Failure reasons
  are localized from stable codes rather than server prose — the verifier and
  the idle sweep now write `txt_not_found`, `txt_mismatch`,
  `dns_lookup_failed`, or `unpinned_idle` into `custom_domains.last_error`,
  with the detail going to the logs.
- **16d** — `router-svc` no longer answers `400` for a foreign host; it
  resolves by full hostname, and `FindRouteByHost` has two branches converging
  on the same running-with-container condition. Unknown, unverified, or
  revoked hosts are `404`, and `/internal/routes` kept its response shape.

Deliberately not done, with the reason:

- **Item 4 (automatic promotion on `running`)** — done 2026-08-06. The saga
  repoints every verified domain of the project onto the deploy that just went
  live, as the last thing `stepCommit` does. One `UPDATE`, so a domain is never
  briefly pointing at nothing, and the previous build serves right up to it:
  that is what makes a redeploy invisible from outside and a failed build
  harmless. Since Task 15b, reaching that point means the deploy actually
  answered on the injected port, which is the guard the whole thing depended on.

  Deliberately best-effort. By then the deploy is running and the coins are
  committed, so a failure logs a warning and publishes one to the deploy log
  rather than failing the saga — the cost of not moving the pointer is a stale
  site, and the cost of treating it as fatal would be tearing down a working,
  paid-for deploy over a bookkeeping update. Same rule Task 13a's log
  collection had to learn.
- **16c (TLS)** — done. The spike chose our own Caddy edge
  ([ADR 0007](../../decisions/0007-custom-domain-tls-edge.md)); it is deployed
  and proven on a real domain. Evidence and the operating detail are in
  [custom-domains.md](../../operations/custom-domains.md).
- **Item 18's identity gate** — implemented as `DOMAIN_ATTACH_REQUIRE_IDENTITY`,
  default off, because no payment method exists to verify. Turning it on
  refuses every attach; until then the per-user count and rate limits plus
  manual review are the abuse controls.
- **16e** — detach and re-verification landed with 16b (they share the
  verifier and the revoke path); emitting domain state into the Task 13b
  feedback path (item 32) did not.
- **16g** — [ADR 0007](../../decisions/0007-custom-domain-tls-edge.md) and the
  [runbook](../../operations/custom-domains.md) are written (items 36 and 38).

**Gap found by the end-to-end proof: archive deploys could not be
re-published — server side fixed 2026-08-06.** Each archive upload created its
own project, because an uploaded tarball has no stable identity. A domain
belongs to a project, so `POST /domains/:id/target` refuses a deploy from a
different one — and every new upload *was* a different one. Publishing a new
build over an attached domain therefore meant detaching and re-attaching, which
regenerates the TXT token and asks the customer to edit DNS again.

That is the path the MCP server and the editor extension use, so it made custom
domains unusable for exactly the audience Task 14 targets. Git-sourced deploys
were never affected — they converge on one project by source key.

`POST /deploys` now takes an optional `project_key`, so a client that can
persist one value between deploys keeps its project. **The client half is not
done**: until the MCP server generates and sends one, agent deploys still land
in a fresh project each time. Tracked as
[Task 14e](0014-vibecoder-ingress.md).

The dashboard groups deploys into projects client-side from
`GET /api/v1/deploys`, which now carries `project_id`. There is no
`GET /api/v1/projects` endpoint yet; one is worth adding when a project grows
anything of its own (a name, settings), but it would be an empty wrapper today.
The consequence to watch: the project page only sees the deploys inside that
one page of results, so a project's history is truncated at whatever the list
query returns.

The `/dashboard/projects` grid is grouped by project: one card per project
showing its newest build, how many deploys it holds, and — when a verified
domain serves it — that hostname instead of the generated URL. Filters still
select deploys, so "запущенные" means a project with a running build. Deploys
predating 16a carry no project and keep the old per-deploy card in a separate
section, since a domain cannot be attached to them.

Per-deploy destructive actions (stop, delete, restart) moved off the grid to
the project page and the deploy modal: on a project card they would silently
act on the newest build only, which is not what "delete this project" reads as.

## Problem

Users get a generated `*.${DOMAIN_SUFFIX}` URL and nothing else. Attaching
their own domain — the thing that turns a preview into a published site — is
not possible.

Every comparable Russian PaaS treats this as a base feature, not a
differentiator: Timeweb Cloud Apps and Onreza both advertise custom domains
with automatic Let's Encrypt / managed certificates, RelaxDev advertises
automatic SSL for attached domains. Shipping without it reads as an incomplete
platform.

It is also where the product stops being a preview tool. A deploy that answers
on the user's own hostname is the thing they show other people — which is what
makes the quality of that serving path (see Decided policy) worth paying for.

## What exists today

- `router-svc` resolves runtime traffic by `Host`, but only after
  `NormalizeHostForDomain` asserts the host ends in `.${DOMAIN_SUFFIX}`
  ([proxy.go](../../../snaphost-backend/router-svc/internal/router/proxy.go)).
  A foreign host is rejected with `400 bad host` before any lookup runs.
- `GET /internal/routes?host=` resolves that host by **stripping the suffix and
  matching `deploys.subdomain`**
  ([repository.go](../../../snaphost-backend/internal/control/deploy/repository.go)
  `FindRouteByHost`). There is no lookup path for an arbitrary hostname.
- TLS terminates at one Yandex API Gateway bound to a single wildcard
  certificate for `*.${domain_name}` + the apex
  ([api_gateway.tf](../../../terraform/yandex/api_gateway.tf),
  [certificate.tf](../../../terraform/yandex/certificate.tf)). That certificate
  covers exactly one domain suffix and cannot cover a user's domain.

## Blocking design gap: nothing stable to point a domain at

`deploys.subdomain` is unique **per deploy** (`uq_deploys_subdomain`,
[0003_deploys.up.sql](../../../snaphost-backend/internal/control/db/migrations/0003_deploys.up.sql)),
and every deploy gets a fresh one. Deploys also expire — `CONTAINER_DEFAULT_TTL_MIN`
is 30 minutes and the watchdog tears them down.

So there is no entity a custom domain can be bound to that survives a redeploy.
Binding a domain to a `deploy_id` would mean the domain goes dead 30 minutes
later and has to be re-pointed on every push. A stable publish target has to
exist first; the rest of this task depends on it.

### The model this converges on

Vercel and Netlify solve this with three layers instead of our two, and the
distinction is worth copying exactly:

| Layer | Lifetime | Mutable | Role |
| --- | --- | --- | --- |
| **Project** | permanent | yes | owner-scoped identity; owns settings and attached domains |
| **Deployment** | long, GC-able | **no** | one build result, permanently addressable at its own generated URL |
| **Alias** | permanent | yes (pointer) | a hostname pointing at *one* deployment |

A domain is a *pointer*, not a property of a deployment. Publishing means
building a new immutable deployment and atomically repointing the alias at it;
rollback means repointing the alias back, with no rebuild. Their deployments
are never reaped on a timer, so "the domain dies with the deploy" cannot
happen.

Our `deploys.subdomain` is already the immutable per-deployment URL — that half
of the model exists. What is missing is the project above it and the alias
beside it.

### What this means for the TTL

The 30-minute TTL was calibrated for the Docker backend, where a container
occupies host RAM for as long as it exists. On Serverless Containers an idle
deployment scales to zero and costs approximately nothing; the standing cost of
keeping a deployment alive is its registry image, not its runtime.

So the TTL does not need to be removed — it needs to become **garbage
collection for un-aliased deployments**. A deployment that an alias points at is
pinned and skipped by the watchdog; everything else expires as it does today.

Note also what is *not* expensive here: a pinned scale-to-zero deployment costs
roughly a rouble a year, while a warm one (`min_instances >= 1`, no cold start)
costs two orders of magnitude more. If a paid tier is drawn around this
feature, the defensible line is warm-vs-cold, not aliased-vs-not.

## Decided policy

Settled 2026-07-25, owner decision:

- **Free-tier preview TTL is 24 hours**, not 30 minutes. Under scale-to-zero the
  cost difference is negligible, and a link that survives the working day is the
  difference between a shareable preview and a demo that expires before anyone
  opens it.
- **Free tier gets one custom domain.** The domain itself costs nothing to
  serve; gating it would put us visibly behind platforms whose free tiers
  include several. Attach is still gated on an identity signal (item 18) —
  abuse, not cost, is what that gate is for.
- The paid line is **warm serving** (`min_instances >= 1`, no cold start), plus
  more domains, more pinned projects, and higher build concurrency. A free
  custom domain therefore serves from a scale-to-zero deployment and answers its
  first request after idling with a cold start; removing that wait is the
  upgrade.

## Prerequisites

P1, P3, and P4 are other tasks; P2 is new work owned by no task today. They gate
different parts of this one, and none of them block starting 16a's data model.

### P1 — Task 15 must land before alias promotion (SATISFIED 2026-07-30)

Task 15b's liveness probe shipped, so `running` now means the deploy answered
on the injected port, and promotion was built on that guard on 2026-08-06. The
original reasoning follows, because it is why the guard exists.

Promotion is defined as "move the alias once the new deployment reaches
`running`". Then, `running` did not mean the deployment answers: a deploy was
marked `running` and its coins are committed with nothing having checked the
public URL. That is the recorded 2026-07-19 incident (`d32c6531…` on staging,
`UserCodeError` on every request while the deploy sat in `running`) and the
reason [Task 15](0015-runtime-port-contract.md) exists.

So the signal the promotion guard depends on is currently unreliable. On a
preview that produces one broken link. On a custom domain it means a redeploy
repoints **the user's own hostname** at a deployment that never serves, seconds
after a working one was live — the exact failure the atomic promotion exists to
prevent. Task 15b's liveness probe is what makes `running` a usable guard.

Everything else in 16a — the tables, the pinning, the GC, the retention policy —
can be built before Task 15 lands. Only item 4 depends on it.

### P2 — Reserve a stable public address before accepting any domain (SATISFIED 2026-07-30)

A static address on a rented server — which replaces the temporary staging VDS
and will also carry production — is now reserved and recorded in
[public-address.md](../../operations/public-address.md): customers CNAME to
`edge.snaphost.pw`, apex domains use the address itself, and the A record is
Terraform-managed from `edge_ip_address`. `DOMAIN_CNAME_TARGET` and
`DOMAIN_A_RECORD_TARGET` are set in the production env, so attach no longer
refuses with `edge_address_unreserved`.

This unblocks 16c but does not replace it: nothing terminates TLS for foreign
hostnames yet (ports 80/443 on the edge are closed), so a domain can be
attached and verified while its traffic is not served. External users must not
be invited to attach domains until 16c ships.

The original reasoning follows.

A custom domain means asking users to point DNS at us, and an apex domain
requires an `A` record to a fixed address. Our staging VDS has been rebuilt and
changed address more than once.

If the address moves after users have pointed DNS at it, **every** apex-domain
customer's site goes dark at once, silently, and only they can fix it at their
own registrar. Whatever 16c chooses, its target — a reserved static IP, or a
gateway hostname stable across rebuilds — has to be reserved and documented
before the first external domain is accepted. This is cheap, and it is currently
in no task.

### P3 — Task 11 launch prerequisites (gates public launch, not development)

[Task 11](0011-production-deployment.md) is still in progress: durable TLS
endpoint configuration, firewall verification, proven rollback and PostgreSQL
restore, an approved production environment, and monitoring all remain open, and
production does not exist yet — only staging.

Inviting users to delegate DNS to infrastructure whose rollback and restore are
unproven is premature. Note also that Task 11's open "durable TLS endpoint"
blocker is the same surface 16c builds on; deciding them independently risks two
conflicting answers.

### P4 — Task 13a makes custom-domain support survivable (strongly recommended)

When a site on a customer's own domain breaks, diagnosis today is what it was on
2026-07-19: pull the image onto the VDS and run it by hand, because no runtime
log group exists.
[Task 13a](0013-observability-and-user-feedback.md) turns that into a log query.
The volume and urgency of "my domain is broken" reports is not the same as for
throwaway previews.

### P5 — Decisions to make before the parts that need them (non-blocking)

- [ ] Run the 16c.0 spike before committing to Option A or B (gates 16c only).
- [ ] Decide whether custom domains work in the local Docker/Traefik path or are
      Yandex-only. Cheap to settle now, expensive to retrofit — it shapes the
      interface in item 23.
- [ ] Decide how item 18's identity gate is satisfied while payment collection
      does not exist: wait for billing, or ship with a weaker signal plus manual
      abuse review.

## Work plan

### 16a — Project, immutable deployment, alias (prerequisite)

1. [x] Add `projects`: `id`, `user_id`, `slug`, `created_at`. Owner-scoped,
   permanent, independent of any single deploy.
2. [x] Add `deploys.project_id`. `deploys.subdomain` keeps its current meaning —
   the immutable URL of that one build — and must stay resolvable after a newer
   deployment supersedes it, because that is what makes rollback a pointer move
   rather than a rebuild.
3. [x] Add the alias table (see 16b — `custom_domains` carries
   `target_deploy_id`; the generated project hostname is the same mechanism with
   a host we own).
4. [ ] **(blocked on P1 / Task 15)** Promotion is a single atomic update of the
   alias target, applied **only after** the new deployment reaches `running` —
   and `running` must by then mean the deployment actually answers. The previous
   deployment keeps serving until that moment, so a redeploy never blanks a live
   site and a failed build never takes one down.
5. [x] Rollback re-points the alias at any earlier deployment of the same
   project that still has a live runtime — no rebuild, no new image.
6. [x] Watchdog: exclude aliased deployments from TTL expiry. Everything
   un-aliased keeps today's behavior, so preview deploys are unaffected.
7. [x] Replace timer-based reaping of pinned deployments with
   **inactivity-based** GC: no traffic for N days → unpin, then remove the
   runtime and the registry image. A hard timer cannot express "this site is
   live but quiet", which is the normal state of a small published site.
8. [x] Retention policy for superseded deployments: keep the last K per project
   (rollback targets), GC the rest. Registry storage is the real accumulating
   cost, not idle runtime.
9. [x] Raise the free-tier preview TTL to 24 hours
   (`CONTAINER_DEFAULT_TTL_MIN=1440`). **This ships independently of the rest of
   this task** — it is an environment value, and nothing in the watchdog needs
   to change to honor it. Keep the local Docker dev value low: there containers
   really do hold host RAM for their whole TTL, which is what the 30-minute
   default was sized for.
10. [x] Make TTL a per-deploy decision rather than a global constant. The wire
    contract already carries it — `ttl_minutes` exists in the saga's runner
    request ([clients.go](../../../snaphost-backend/internal/control/saga/clients.go))
    and `runner-svc` honors any non-zero value over its config default — but
    nothing ever sets it. The saga should fill it from the deploy's tier.
11. [x] Enforce the tier's TTL ceiling in `user-billing`, not `runner-svc`.
    Runner applies whatever non-zero `ttl_minutes` it receives with no upper
    bound, so the limit has to be applied where the tier is known. The endpoint
    is internal-only (`X-Webhook-Secret`), so this is correctness for the tier
    model rather than a public attack surface — but the ceiling belongs with the
    entitlement, not with the executor.

### 16b — Domain registration and ownership verification (user-billing)

12. [x] Migration `00NN_custom_domains`: `id`, `user_id`, `project_id`,
   `target_deploy_id`, `domain` (normalized lowercase, globally unique),
   `verification_token`, `status` (`pending` | `verified` | `failed` |
   `revoked`), `verified_at`, `last_checked_at`, `created_at`. This table is the
   alias layer from 16a — `target_deploy_id` is the pointer that promotion and
   rollback move.
13. [x] Public API behind api-gateway: `POST /api/v1/domains` (attach),
    `GET /api/v1/domains`, `DELETE /api/v1/domains/:id`. Add matching
    `rbac_policy.csv` lines — the Casbin middleware enforces on the real URL
    path, so a missing policy line fails the route.
14. [x] Attach returns the DNS instructions: a `TXT` challenge at
    `_snaphost-verify.<domain>` containing a per-domain random token, plus the
    `CNAME`/`A` target to point at.
15. [x] Verification job resolves the TXT record and flips `pending` →
    `verified`. **A domain must never route traffic before it is verified** —
    without proof of ownership, any user could attach a hostname someone else
    already controls, and on an on-demand-TLS edge could also drive certificate
    issuance for it.
16. [x] Reject at attach time: hosts under `${DOMAIN_SUFFIX}` (that space is
    ours), duplicates of an already-verified domain, and syntactically invalid
    names.
17. [x] Rate-limit attach per user with the existing Redis limiter; each attach
    costs DNS lookups and, on the edge, an ACME issuance attempt.
18. [ ] Gate attach on an identity signal (verified payment method or paid
    tier), independent of whether the feature itself is priced. The feature is
    free (see Decided policy), but free custom domains on a hosting platform
    attract phishing and spam, and the cost lands on the reputation of our edge
    address for every other user. Abuse, not cost, is what this gate is for.
    Enforce the per-tier domain count here too — one on the free tier — and
    return a reason specific enough for the dashboard to explain the refusal.

### 16c — TLS termination for foreign hostnames (decision + spike)

The single wildcard certificate cannot serve a user's domain, so this is the
part with real design risk. Two candidate paths — **run 16c.0 before
committing**, and satisfy P2 (a reserved, rebuild-stable public address) before
either ships:

19. [x] **16c.0 spike — done 2026-08-04.** Findings, and the decision they
    produced, are in [ADR 0007](../../decisions/0007-custom-domain-tls-edge.md).
    In short:

    - **Runtime attach is possible.** `AddDomain`/`RemoveDomain` RPCs exist on
      the API Gateway service and take `(api_gateway_id, domain_name,
      certificate_id)`. That was the open question for Option A, and the answer
      is yes.
    - **Neither challenge type works cleanly for a zone we do not control.**
      Certificate Manager offers `DNS` and `HTTP`. DNS needs the customer to
      publish and keep a second record forever, because managed certificates
      re-validate on renewal. HTTP needs `:80` on *their* hostname to reach us
      before the domain is attached — which the gateway cannot do, and which
      only an edge of our own can. Option A's "no new component" claim does not
      survive its own validation step.
    - **The edge already exists.** Caddy v2.11.4 is enabled on the production
      host, listening on `*:80` and `*:443`, terminating TLS for the control
      plane. Option B was costed as new construction; it is assembly.
    - **`router-svc` already runs outside Yandex.** It defaults to
      `USER_BILLING_URL=http://user-billing:8081` and authenticates with
      `YANDEX_AUTH_MODE=key_file` against a runner key already on the VDS. No
      new secret, no metadata dependency, image already in the CI matrix.
    - **Per-gateway domain ceiling was not measured**, because the other
      findings decide the option without it. It stays unknown, and it stays a
      reason not to choose Option A later without re-measuring.
    - **Issuance latency** for the chosen path is ACME-immediate rather than
      Certificate Manager's managed flow, so it was not measured either.

20. [~] **Option A — Yandex-native.** Rejected by the spike; reasoning recorded
    in [ADR 0007](../../decisions/0007-custom-domain-tls-edge.md). Kept here
    because the per-gateway domain ceiling was never measured, so anyone
    revisiting this needs to measure it first.

21. [x] **Option B — own TLS edge. Chosen, built, deployed, and proven.** Caddy on the production host
    obtains a certificate per verified domain through gated on-demand ACME
    issuance and proxies to `router-svc` running on the same host, which
    resolves the hostname through `user-billing` and signs the upstream call to
    the user's Serverless Container exactly as it does today.

    The generated-hostname path is untouched: `*.snaphost.pw` keeps going
    through the Yandex API Gateway. Custom domains become a second, parallel
    ingress, and it must not be able to affect deploys served on generated
    hostnames — the lesson Task 13a's log collection produced on 2026-08-04,
    when an auxiliary dependency was allowed to break the deploy path.

    Built 2026-08-04, **not deployed**:

    - [x] `GET /internal/tls/authorize?domain=` on `user-billing` (item 22);
    - [x] the same probe re-exposed by `router-svc` on its loopback port.
      Caddy's `ask` is a bare URL and cannot carry the shared secret, so the
      endpoint the edge calls cannot require one. Putting it on `router-svc`
      keeps that unauthenticated surface on loopback — `user-billing` publishes
      no host port at all — and the secret-bearing hop happens behind it. It is
      gated by `TLS_ASK_ENABLED`, off by default, because the Yandex instance of
      the same image sits behind a public gateway. It also refuses our own
      suffix without consulting billing, so it cannot be used to request a
      duplicate certificate for a generated hostname;
    - [x] `router-svc` as a VDS Compose service on `127.0.0.1:${ROUTER_BIND_PORT}`,
      using the runner key already on the host — no new secret;
    - [x] [Caddyfile.production.example](../../../infra/Caddyfile.production.example)
      with `on_demand_tls { ask }` and a catch-all `:443` block preserving
      `Host`. Validated against Caddy 2.11, which **removed** the on-demand
      `interval`/`burst` rate limit — so `ask` is the only bound at the edge and
      `MAX_DOMAINS_PER_USER` / `DOMAIN_ATTACH_PER_HOUR` are now ACME controls;
    - [x] runbook (item 38).

    Remaining:

    - deploy it: `router-svc` has never run on the VDS and the Caddyfile is an
      example, not the installed config;
    - certificate storage that survives a host rebuild — decide backup or
      accept a mass re-issue;
    - evidence for one real domain end to end.

22. [x] The on-demand TLS `ask` endpoint consults `custom_domains` and answers
    only for `verified` rows. An open `ask` endpoint lets any host that resolves
    to the edge trigger certificate issuance, which burns ACME rate limits and
    is trivially abusable.

    Implemented as `GET /internal/tls/authorize?domain=`
    ([tls.go](../../../snaphost-backend/internal/control/domain/tls.go)),
    behind the shared webhook secret like every other internal route. It
    normalizes the host exactly as attach does, so `Example.COM.` cannot slip
    past as an unverified name; **fails closed** on a database error, because a
    blip must not become an open issuance gate; and returns an empty body,
    since the far side of that question is whoever pointed DNS at us. Six test
    cases cover allow, refuse, fail-closed, missing input, normalization, and
    body silence.

    Deliberately not consulted: whether the target deploy is running. A
    verified domain whose deploy is down should keep its certificate and answer
    with the router's `404` — dropping it would turn a dead page into a browser
    TLS warning and cost an ACME round trip to undo.
23. [ ] Whichever option wins, put certificate provisioning behind an interface
    the way runner backends are (ADR 0004), so the Docker/Traefik dev path and
    the Yandex path do not fork the core.
24. [x] Reserve the public address users will point DNS at (P2) and record it in
    the operations docs as a value that must survive a host rebuild. Publish the
    `CNAME` target and, for apex domains, the `A` address only once it is
    reserved — a published address that later moves breaks every customer's site
    at once, and only they can repair it.

### 16d — Route resolution for custom hosts

25. [x] Relax `NormalizeHostForDomain` in `router-svc`: a host outside
    `${DOMAIN_SUFFIX}` is no longer a `400`, it is a lookup by full hostname.
    Keep normalization (lowercase, strip port and trailing dot) identical for
    both paths.
26. [x] Extend `FindRouteByHost` into two branches that converge on the same
    result. Suffix hosts keep today's `deploys.subdomain` match — that is the
    immutable per-deployment URL and must not change behavior. Other hosts
    resolve through `custom_domains` (`status = 'verified'`) to
    `target_deploy_id`. Both branches then apply the same final condition:
    `status = 'running'` with a non-empty `container_id`.
27. [x] Unknown, unverified, or revoked host resolves to `ErrRouteNotFound` →
    `404`, matching today's behavior for a dead subdomain.
28. [x] Keep the response shape of `/internal/routes` unchanged so `router-svc`
    needs no contract change beyond the host check.

### 16e — Lifecycle, revocation, re-verification

29. [x] Detaching a domain, or deleting a project, clears the alias and (Option
    B) stops answering `ask` for it. A revoked domain must stop resolving
    immediately, not at the next cache expiry.
30. [x] Detaching an alias unpins its target deployment, which returns it to
    normal GC. Nothing should stay pinned because a domain that no longer exists
    once pointed at it.
31. [x] Re-verify verified domains periodically in the existing watchdog style.
    A domain that stops pointing at us — or is transferred to someone else —
    must lose `verified` status, otherwise we keep serving a hostname whose
    owner has moved on (dangling-record takeover).
32. [ ] Emit the domain state changes into the Task 13b user-feedback path so
    "DNS not propagated yet" and "certificate issued" are visible in the
    dashboard rather than silent.

### 16f — Dashboard

33. [x] Domains page: attach form, copyable DNS records (TXT challenge and
    CNAME/A target), live status (`pending` → `verified` → `active`) with
    polling, detach with confirmation.
34. [x] Surface the failure reasons plainly: record not found yet, wrong value,
    apex-domain limitation (a bare `example.com` frequently cannot hold a
    `CNAME`; document the A-record path for it).
35. [x] Project view: list the project's deployments with their immutable URLs,
    mark which one the domain currently points at, and offer rollback as an
    explicit repoint action.

### 16g — Documentation

36. [ ] ADR for the 16c decision — this changes where TLS terminates, which is
    exactly the class of decision `docs/decisions/` exists for. Note explicitly
    how it relates to ADR 0003 (Terraform owns the gateway) and ADR 0004
    (provider boundary).
37. [x] ADR or architecture note for the project/deployment/alias split — it
    changes the deploy lifecycle described in
    [deploy-lifecycle.md](../../architecture/deploy-lifecycle.md), including
    what the watchdog is allowed to reap.
38. [x] Operations runbook —
    [custom-domains.md](../../operations/custom-domains.md): the request path,
    host prerequisites, symptom-by-symptom diagnosis down that path, revocation,
    and the deliberate behaviours that look like bugs (a down deploy keeps its
    certificate; the `ask` endpoint is unauthenticated by necessity). It ends
    with what has not been deployed or proven, so it does not read as a
    description of something already running.

## Open questions

- Yandex API Gateway: runtime domain attach supported, and what is the
  per-gateway domain ceiling? (16c.0 — decides Option A viability.)
- Certificate Manager challenge types usable for a zone we do not control, and
  issuance latency.
- What "warm serving" costs to *offer* rather than to run: `min_instances >= 1`
  is priced per warm instance, so the paid tier's margin depends on how many
  warm projects one subscription is allowed to hold. Decide the entitlement
  before the price.
- How the identity gate in item 18 is satisfied before payment collection
  exists. If nothing can verify a payment method yet, the free custom domain
  either waits for that, or ships behind a weaker signal with an explicit abuse
  review.
- Which tier ceilings the saga enforces (item 11) beyond TTL: pinned projects
  per user, domains per user, concurrent builds.
- Apex domains: support via A record to a static address, or document
  `www`-only and redirect? Option B has a stable edge address; Option A depends
  on what the gateway exposes.
- How many deployments to retain per project (item 8) before registry storage
  becomes the dominant per-user cost.

## Acceptance criteria

- A user attaches a domain, adds the published TXT record, and the domain
  reaches `verified` without operator involvement.
- An unverified or revoked domain never routes traffic and never triggers
  certificate issuance.
- A verified domain serves the deployment its alias points at over HTTPS with a
  valid certificate.
- A redeploy is invisible from outside: the previous deployment answers every
  request until the new one is `running`, then the alias moves. A failed build
  leaves the live site untouched.
- Rolling back is a repoint — an earlier deployment of the same project serves
  the domain again with no rebuild and no new image.
- An aliased deployment survives the TTL watchdog; an un-aliased one expires on
  the tier's TTL — 24 hours on the free tier, not the current 30 minutes.
- A free-tier account can attach exactly one custom domain; the second attach is
  refused with a reason the dashboard shows, not a generic error.
- A generated `*.${DOMAIN_SUFFIX}` URL behaves exactly as before — same
  normalization, same `404` for unknown hosts — and a superseded deployment
  stays reachable at its own URL.
- Detaching a domain stops it resolving; a domain whose DNS stops pointing at
  us loses `verified` on the next check.
- Staging evidence recorded for one real domain end to end, per the project's
  smoke-proof convention.

## Out of scope

- Domain registration or resale — users bring a domain they already own.
- Wildcard custom domains (`*.userdomain.com`).
- Per-domain WAF, CDN, or caching.
- Router route caching, which stays as its backlog item with its own
  invalidation semantics — note that custom-domain revocation makes staleness
  a correctness problem, not just a performance one.

## Notes

Rough size: ~3 weeks for one developer, with the risk concentrated in 16c (a
new TLS termination point) and 16a (project/deployment/alias reshapes the deploy
lifecycle and the watchdog's authority). 16b, 16d, and 16f extend patterns that
already exist.

16a is worth doing whether or not custom domains ship. Immutable deployments
plus a movable alias are also what pointer-based rollback, zero-downtime
redeploys, and stable per-project URLs all require; the custom domain is one
consumer of that model, not its only justification.

Real payment collection is the other open gap and gates revenue, while this
gates the product's credibility against comparable platforms. They do not block
each other technically — except that item 18 gates domain attach on an identity
signal, which is easiest once payment methods exist.

## Suggested order (original plan — all four steps have since happened)

Kept because it records why the work was sequenced this way, not because
anything here is still pending. Step 4's preconditions did hold: P2 was
satisfied on 2026-07-30 and 16c shipped after it.

1. **Now, unblocked:** item 9 (`CONTAINER_DEFAULT_TTL_MIN=1440`) ships on its
   own. Start 16a's data model — everything except item 4. Run the 16c.0 spike
   in parallel, since it decides 16c and nothing waits on it.
2. **Then:** Task 15, which unblocks item 4 and with it the whole promotion and
   rollback story.
3. **Then:** 16b and 16d — the domain table, verification, and host resolution.
4. **Not before P2 and P3:** 16c and any public availability of the feature. The
   address users point DNS at has to be reserved first, and Task 11's launch
   prerequisites have to close, because the failure mode of getting either wrong
   lands on domains we do not control and cannot repair.
