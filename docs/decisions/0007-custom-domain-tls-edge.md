# ADR 0007 — TLS for customer domains terminates at our own edge

Status: Accepted
Date: 2026-08-04

## Context

A verified custom domain does not serve traffic today. The single Yandex
Certificate Manager wildcard covers `*.snaphost.pw` and structurally cannot
cover a hostname we do not own, so nothing terminates TLS for a customer's
domain. [Task 16](../tasks/active/0016-custom-domains.md) scoped two candidate
paths and required a spike (16c.0) before committing to either.

The spike ran on 2026-08-04. What it found:

**Yandex API Gateway can attach domains at runtime.** `AddDomain` and
`RemoveDomain` RPCs exist and take `(api_gateway_id, domain_name,
certificate_id)`. Option A is therefore technically possible, which was the
open question.

**Certificate Manager offers DNS and HTTP challenges** — and neither works
cleanly for a zone we do not control:

- *DNS* requires the customer to publish a second record
  (`_acme-challenge.<domain>`) beyond the one pointing at us, and to keep it
  forever, because managed certificates re-validate on renewal. Comparable
  platforms ask for one record.
- *HTTP* requires `http://<their domain>/.well-known/acme-challenge/<token>` to
  reach us. The gateway cannot serve a domain that is not attached yet, and the
  domain cannot be attached without a certificate. The only way out is an edge
  that already terminates `:80` for arbitrary hostnames — which is Option B's
  component. Option A's central claim, "no new component", does not survive its
  own validation step.

**The edge already exists.** Caddy v2.11.4 runs on the production host,
enabled, listening on `*:80` and `*:443`, terminating TLS for `snaphost.ru`,
`www`, and `api`. Option B was scoped as "a new deployable component with a
public address"; that component is deployed and serving.

**`router-svc` already runs anywhere.** Its config defaults to
`USER_BILLING_URL=http://user-billing:8081` and it authenticates with
`YANDEX_AUTH_MODE=key_file` against a runner key already present on the VDS at
`/opt/snaphost/secrets/runner-key.json`. It needs no metadata service and no
new secret. Its image is already in the CI publish matrix.

## Decision

Terminate TLS for customer domains at our own edge (Option B).

Caddy on the production host obtains a certificate per verified domain through
ACME on-demand issuance and proxies to `router-svc` running on the same host,
which resolves the hostname through `user-billing` and signs the upstream call
to the user's Serverless Container exactly as it does today for generated
hostnames.

On-demand issuance **must** be gated by an `ask` endpoint that answers only for
rows in `custom_domains` with `status = 'verified'`. An open `ask` lets any
hostname resolving to the edge trigger issuance, which burns ACME rate limits
and is trivially abusable.

The generated-hostname path (`*.snaphost.pw` → Yandex API Gateway →
`router-svc`) is unchanged. Custom domains are a second, parallel ingress.

## Consequences

- Issuance is immediate and needs one DNS record from the customer, the one
  they already add. No per-domain Terraform resource, no per-gateway domain
  quota, no runtime mutation of a Terraform-owned gateway.
- It keeps the direction of [ADR 0003](0003-central-router.md) rather than
  reversing it. That ADR removed per-deploy gateway mutation because it raced
  and drifted; attaching a gateway domain per customer would reintroduce the
  same class of problem one layer up.
- Certificate provisioning stays behind the runtime boundary
  ([ADR 0004](0004-provider-boundary.md)): the core learns nothing about
  Certificate Manager, and the Docker/Traefik development path can use the same
  `ask` contract.
- **A second ingress is a second failure domain.** It carries only
  custom-domain traffic — a broken edge must not affect deploys served on
  generated hostnames, and must not be able to fail a deploy. This is the same
  rule that Task 13a's log collection had to learn on 2026-08-04, when an
  auxiliary dependency was allowed to break the deploy path.
- Let's Encrypt rate limits now apply per customer registrable domain rather
  than to ours, which is the correct blast radius. The `ask` gate and the
  existing per-user attach limits are what keep issuance attempts bounded.
- The edge host becomes stateful in one new way: Caddy's certificate storage
  must survive a host rebuild, or every customer domain re-issues at once.
- Caddy currently runs with `admin off` and a root-owned `Caddyfile`, so the
  configuration is a host prerequisite rather than something CD manages. That
  boundary is deliberate and is kept.

## Alternatives considered

**Option A — Yandex-native.** Provision a managed certificate per verified
domain and `AddDomain` it to the existing gateway. Rejected: the validation
chicken-and-egg above, runtime mutation of a Terraform-owned resource, an
undetermined per-gateway domain ceiling, and provider-specific certificate
logic that ADR 0004 puts behind a boundary. Its one advantage — no new
component — turned out to be untrue in both directions: it needs an HTTP-
challenge edge, and the edge already exists.

**Wildcard certificate per customer domain.** Not applicable; we cannot prove
control of their zone without the same challenge problem.
