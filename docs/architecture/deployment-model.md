# Deployment model

Status: Current Phase 1 production contract; rollout not implemented
Type: Architecture
Updated: 2026-07-30

## Domain split

Two registrable domains, and the split is load-bearing rather than cosmetic:

| Domain | Serves | Config |
| --- | --- | --- |
| `snaphost.ru` | dashboard (apex, `www`) and public API (`api.`) | `CORS_ALLOW_ORIGINS`, `VITE_API_URL`, Supabase Site/Redirect URLs |
| `snaphost.pw` | user deploys (`proj-<id>.`), the custom-domain edge (`edge.`) | `DOMAIN_SUFFIX`, wildcard certificate, API Gateway |

`snaphost.online` served deploys until 2026-08-03 and is parked as a spare. It
stays in `RESERVED_DOMAINS` so it cannot be attached as a custom domain.

Why not one domain with two subdomains:

- **Cookies.** Browsers scope cookies per registrable domain. A deployed
  application on `x.snaphost.ru` could set a cookie with `Domain=snaphost.ru`,
  which the dashboard would then receive — cookie tossing and session fixation.
  A separate domain removes the class of attack rather than mitigating it.
- **Storage and site isolation.** The deploy suffix should additionally be
  submitted to the [Public Suffix List](https://publicsuffix.org/submit/), which
  is what makes browsers treat `proj-a` and `proj-b` under it as separate sites.
  Without that entry one user's deploy can set cookies for every other user's
  deploy. This is why `*.vercel.app` and `*.netlify.app` are on the PSL, and it
  is not yet done here — see the stopgap below.

Note what is *not* shared even without a PSL entry: `localStorage`,
`sessionStorage`, and IndexedDB are keyed by origin, so two deploys never see
each other's storage. The exposure is cookies, and only between deploys on the
generated suffix — a custom domain belongs to one customer, so there is no other
tenant on it.

### Stopgap until the PSL entry lands

A PSL submission takes months and can be refused, so `router-svc` strips the
`Domain` attribute from every `Set-Cookie` a deploy returns on a generated
hostname ([cookies.go](../../snaphost-backend/router-svc/internal/router/cookies.go)).
The cookie becomes host-only, which is what a single-hostname application needs
anyway, and cannot be read by another tenant. Custom domains are left untouched:
the whole registrable domain is the customer's, and sharing a cookie with their
own `www` is legitimate.

Two gaps remain, and they are why the PSL entry is still the real fix:

- cookies written by page JavaScript (`document.cookie = "…; domain=…"`) never
  reach the proxy and cannot be rewritten there;
- the local Docker path routes through Traefik straight to the container, so
  the stopgap does not apply in development.
- **Reputation.** Free hosting attracts phishing. Blocklists (Safe Browsing,
  corporate proxies, mail filters) act on domains, so a report against a user's
  deploy must not be able to take the dashboard and API offline with it.
- **Simpler CORS and CSP.** The API has exactly one allowed browser origin, and
  it is never a host users control.

Both domains resolve to the same host today
([public address](../operations/public-address.md)); the split is at the DNS and
cookie layer, not the hardware layer, and either can be moved independently.

`user-billing` refuses to attach any hostname under either domain:
`DOMAIN_SUFFIX` is rejected because that namespace is ours to allocate, and
`RESERVED_DOMAINS` (the control-plane domain) because that is where sessions
live.

## Local development

Docker Compose runs the control plane, PostgreSQL, Redis, BuildKit, Registry,
Traefik, and user containers on one Docker host. The local Compose file is not
a production manifest.

## Production placement

The persistent VDS runs `api-gateway`, `user-billing`, `builder-api`,
`builder-worker`, `runner-api`, `runner-watchdog`, `ai-orchestrator`, PostgreSQL,
Redis, and BuildKit from `infra/docker-compose.prod.yml`. The production runner
uses `RUNNER_BACKEND=yandex` and central router mode. The VDS does not run a
local Registry, Traefik, `router-svc`, or user containers.

Yandex Cloud runs user Serverless Containers and `router-svc`, plus API
Gateway, wildcard DNS, certificate, Lockbox, service accounts, and Container
Registry. Terraform owns those cloud resources. Compose owns only VDS
containers, networks, and named volumes and does not mutate Yandex resources.

## Endpoints and connections

The only VDS host port is the API gateway HTTP port, configured by
`API_GATEWAY_BIND_ADDRESS` and `API_GATEWAY_PORT`. TLS termination and firewall
policy remain operator prerequisites. PostgreSQL, Redis, BuildKit, billing,
builder, runner, and AI endpoints are not published.

| Consumer | Internal endpoint | Purpose |
| --- | --- | --- |
| API gateway | `http://user-billing:8081` | Public API implementation |
| API gateway | `http://ai-orchestrator:8083` | AI API implementation |
| user-billing | `http://builder-api:8082` | Build requests |
| user-billing | `http://runner-api:8084` | Runtime lifecycle |
| builder worker | `tcp://buildkitd:1234` | Image builds |
| control plane | `redis://redis:6379` | Queue, events, and logs |
| database clients | `postgres:5432` | Durable state |

User application traffic enters Yandex API Gateway and is proxied by
`router-svc` to a Serverless Container. For route lookup, `router-svc` calls
`GET /internal/routes?host=...` on the public HTTPS control-plane/API gateway
base URL. API gateway exposes only that exact internal route, validates
`X-Webhook-Secret` with a constant-time comparison, and proxies it to internal
`user-billing:8081`. No other `/internal/*` billing route is exposed, and
user-billing still has no host port. The shared secret reaches router through
Lockbox. Production TLS reachability and firewall policy remain to be verified
on the durable VDS.

## State, credentials, networks, and health

PostgreSQL data, Redis AOF data, and BuildKit cache use named volumes. User
images persist in Yandex Container Registry. Application records persist in
PostgreSQL; build queues/events and deploy logs use Redis.

Backend images use a configurable GHCR prefix and one 40-character Git SHA via
`SNAPHOST_VERSION`. External authorized-key JSON files are mounted read-only:
the builder key only in `builder-worker`, and the runner key only in
`runner-api` and `runner-watchdog`. Terraform bootstrap credentials are never
mounted. Cloud `router-svc` uses metadata authentication and Lockbox.

The internal `data` network carries PostgreSQL, Redis, and BuildKit traffic.
BuildKit joins `data` so `builder-worker` can reach it at
`tcp://buildkitd:1234`; it also joins `control` for outbound base-image pulls
and pushes to Yandex Container Registry. PostgreSQL and Redis remain only on
`data`. Other services that need outbound cloud access also join `control`.

Compose healthchecks are configured for PostgreSQL (`pg_isready`), Redis
(`redis-cli ping`), BuildKit (`buildctl debug workers`), and builder API (its
installed `curl` against `/health`). These commands match the known image
contents, and Compose config validation passes. Runtime health transitions have
not been verified by starting containers. Distroless services and the runner
image contain no HTTP probe utility, so Phase 1 does not add fictional
healthchecks or packages solely for probing. A minimal follow-up should add a
static probe or native healthcheck subcommand.

Deployment scripts, rollback, staging, CD, backups, and live verification stay
outside Phase 1 and are tracked by [Task 11](../tasks/active/0011-production-deployment.md).
