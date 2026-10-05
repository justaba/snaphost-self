# ADR 0007 — Terminate project-domain TLS at an operator-owned Caddy edge

Status: Accepted; implemented
Date: 2026-08-04
Updated: 2026-10-05

## Context

Each project is published on an operator-supplied domain. The platform must
issue a certificate only after ownership verification stored in
`custom_domains` succeeds, and it must move that domain between deploys without
coupling certificates to a container.

An earlier revision also allocated generated names under `DOMAIN_SUFFIX` and
selected a Cloudflare DNS-01 wildcard. The operator does not need generated
production names, a DNS-provider integration or external S3-compatible TLS
backup, so that policy and its secrets have been removed.

## Decision

Terminate TLS at an operator-owned Caddy edge:

1. the control-plane hostname is a fixed Caddy site with normal automatic
   HTTPS;
2. verified project domains use on-demand ACME issuance;
3. Caddy's global `ask` endpoint returns 2xx only when the exact hostname maps
   to a verified alias with a currently running target;
4. after TLS termination, the application edge proxy resolves that hostname to
   the running Docker container stored for its alias target.

Production uses the digest-pinned stock Caddy 2.10.2 image. No DNS module,
provider token or startup plugin installation is part of the deployment.
Project domains must have public A/AAAA or CNAME records pointing at the edge so
HTTP-01 or TLS-ALPN validation can succeed.

The two application listeners remain separate from the operator API, are
available only on the Compose control network and expose no general internal
service API. A database error refuses certificate issuance rather than failing
open. Caddy receives no Docker socket.

Production deploys have no generated public URL. The runtime keeps its stable
internal deploy slug, but `endpoint_url` is null until a project domain is used.
Local development may opt into Traefik hostnames with `DEV_DOMAIN_SUFFIX`; that
setting is absent from production.

Caddy account, certificate and private-key state persists under
`/opt/snaphost/state/caddy`. The operator accepts that loss of this local state
causes Caddy to create a new ACME account and reissue project certificates.
External TLS backup is optional operational work and is not a Task 4 acceptance
requirement.

The [2026-10-05 public rehearsal](../operations/rehearsals/2026-10-05-public-caddy-control.md)
confirmed fixed-host control and on-demand project certificates from separate
staging and production ACME state, fail-closed `ask`, alias changes without a
Caddy reload, and certificate reuse from a local encrypted restore. The
supported SemVer installer path remains a separate release acceptance gate.

## Consequences

- There is no `DOMAIN_SUFFIX`, wildcard certificate or Cloudflare credential.
- Every public project needs its own verified DNS name before it has a public
  URL.
- The control domain is reserved and cannot be attached as a project domain.
- Alias promotion and rollback are database pointer updates and need no Caddy
  reload.
- Unknown, pending, revoked, stopped and targetless domains fail closed.
- On-demand TLS without a working authorization gate remains forbidden.
- Caddy is a public stateful service and only it publishes host ports 80/443.
- Loss of local Caddy state can cause certificate reissuance and is an accepted
  operator tradeoff while no external backup is configured.

## Alternatives

Generated names plus a DNS-01 wildcard were removed because this installation
publishes only explicit project domains. Keeping that path would retain a DNS
provider dependency and a privileged token without a product use.

A certificate per domain in a cloud gateway remains rejected because it
reintroduces provider lifecycle work and still needs an ownership challenge.

Adding Caddy labels directly to deploy containers is insufficient: an alias
can move between deploys and certificate authorization happens before a
container is selected. Routing must consume the durable alias model.

## Caddy references

- [Automatic HTTPS](https://caddyserver.com/docs/automatic-https)
- [On-demand TLS `ask`](https://caddyserver.com/docs/caddyfile/options#on_demand_tls)
