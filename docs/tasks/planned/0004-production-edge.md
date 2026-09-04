# Task 0004 — Production edge

Status: In progress
Updated: 2026-09-04

## Goal

Serve the operator control plane, generated deploy hostnames and verified
custom domains through an operator-owned Caddy process without giving Caddy the
Docker socket or reopening a general service-to-service HTTP API.

## Implemented slice

- The monolith listens on three independent ports: the operator API/panel,
  a data-plane reverse proxy and a Caddy on-demand TLS `ask` endpoint.
- Production Compose runs a digest-pinned Caddy service as the sole owner of
  host ports 80/443. The two application edge listeners remain internal.
- The edge resolver reads SQLite and accepts only an exact generated hostname
  or a verified custom-domain alias whose target deploy is running and has a
  recorded container and application port.
- Upstreams are derived as `snaphost-deploy-<deploy-id>:<app-port>` on the
  shared Docker network. Request data and the stored `endpoint_url` cannot
  select an arbitrary upstream.
- Every request is resolved independently, so alias promotion and rollback take
  effect without a Caddy reload or a stale routing cache.
- `infra/Caddyfile.production.example` is a syntactically valid Caddy 2.10.2
  configuration for the control plane and the shared generated/custom-domain
  path.
- `snaphostctl` creates protected persistent Caddy state, configures the panel
  hostname and ACME email, and includes Caddy in install, upgrade and rollback.

Unknown, pending, revoked, stopped and targetless domains fail closed. A
database error denies certificate issuance with 503 rather than permitting it.
The TLS gate has an empty response body and is exposed only at `/tls/ask` on its
dedicated listener.

## Verification recorded

- All Go tests and `go vet` pass; golangci-lint reports no findings.
- The deployment suite passes 60 scenarios, including exclusive Caddy
  ownership of 80/443, internal edge listeners and matching Caddy upstreams.
- Caddy 2.10.2 validates and formats the shipped Caddyfile, and production
  Compose renders successfully.
- The pinned Caddy image starts under the production read-only filesystem,
  dropped-capability and no-new-privileges constraints and answers its admin
  health endpoint. No host ports were published during this isolated check.
- An isolated Docker-network rehearsal started the real application image and
  a container named with the runtime convention. Generated and verified custom
  hosts were authorized and proxied through Docker DNS; pending and unknown
  hosts were denied, and the temporary containers and image were removed.

This proves the application and local Docker boundary, not public certificate
issuance.

## Remaining acceptance work

1. Define and test the production certificate strategy for generated
   hostnames. Issuing one on-demand ACME certificate for every preview is
   functional but can exhaust CA rate limits; a wildcard certificate normally
   requires a DNS challenge and operator-specific credentials.
2. Exercise control-plane HTTPS, a generated hostname, custom-domain
   verification, certificate issuance, alias promotion, rollback and detach on
   a real VPS with operator-owned DNS.
3. Include Caddy state in the proved encrypted off-host backup and restore
   rehearsal.

Local development deliberately keeps Traefik for convenient generated-host
routing. Removing it from the intended production path does not require making
local development depend on public DNS or ACME.

See [ADR 0007](../../decisions/0007-custom-domain-tls-edge.md) and the
[custom-domain runbook](../../operations/custom-domains.md).
