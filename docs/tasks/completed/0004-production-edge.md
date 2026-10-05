# Task 0004 — Production edge

Status: Completed
Updated: 2026-10-05

## Goal

Serve the operator control plane and one verified domain per project through an
operator-owned Caddy process. Production does not allocate generated deploy
hostnames and does not require a wildcard certificate, DNS-provider API token
or external object store.

## Implemented slice

- Caddy is the only production service publishing 80/443 and has no Docker
  socket.
- The control-plane hostname uses ordinary fixed-host automatic HTTPS.
- Project domains use on-demand ACME with the fail-closed
  `http://snaphost:8082/tls/ask` authorization endpoint.
- The edge resolver accepts only a verified domain alias whose target deploy is
  running and has a recorded container and application port. Unknown, pending,
  revoked, stopped and targetless domains are denied.
- Upstreams are constructed as
  `snaphost-deploy-<deploy-id>:<app-port>`; neither request input nor the stored
  `endpoint_url` can choose an arbitrary target.
- Alias promotion, rollback and detach take effect on the next request without
  a Caddy reload.
- Production uses the digest-pinned stock Caddy 2.10.2 image. There is no
  custom DNS module, plugin download or DNS-provider secret.
- `DOMAIN_SUFFIX` is absent from production configuration. A deploy has no
  public URL until a verified project domain points to it. Local Traefik
  convenience routing is isolated behind `DEV_DOMAIN_SUFFIX`.
- The self-host default `MAX_DOMAINS_PER_USER=0` removes the global quota so
  each project can have its own domain. A positive operator value restores a
  cap.
- Caddy state persists at `/opt/snaphost/state/caddy`. External S3-compatible
  TLS backup and recovery are outside this installation contract.

## Verification recorded

- Go package checks pass for the edge, domain and runtime changes.
- Deployment, installer and backup shell suites pass 62/62, 31/31 and 41/41
  scenarios.
- The panel passes 47 tests, lint, formatting, TypeScript compilation and its
  production build.
- The production Compose file renders without `DOMAIN_SUFFIX`, a DNS secret or
  a custom Caddy build. Only Caddy owns 80/443.
- The stock pinned image validates the production Caddyfile.
- `infra/tests/caddy_integration_test.sh` performs real TLS handshakes: control
  200, verified project domain 200, unknown/pending/revoked/stopped SNI denial,
  alias A→B→A without reload and detach 404.
- Restarting Caddy against the same persistent `/data` store serves the same
  project certificate fingerprint without a new order.
- The [2026-09-26 VDS rehearsal](../../operations/rehearsals/2026-09-26-production-edge-vds.md)
  validates the packaged Compose/Caddy configuration on Ubuntu 26.04 and the
  external path through public TCP 80/443. It records control/project 200,
  unknown-SNI refusal, alias A→B→A, detach 404 and certificate reuse after a
  restart, then removes every temporary listener and container.
- The earlier local and VPS rehearsals exercised the previous wildcard design.
  They are retained as historical records and are superseded by ADR 0007 as
  updated on 2026-09-24.
- The [2026-10-05 public Caddy rehearsal](../../operations/rehearsals/2026-10-05-public-caddy-control.md)
  used operator-owned DNS and recorded staging and trusted production
  certificates for `snaphost.ru` and `kinocassa.ru`, HTTP 200 for panel and
  project, unknown and pending SNI denial, alias A→B→A→B without Caddy reload,
  stopped and detached denial, and project certificate reuse after Caddy
  recreation.
- `backup.sh tls` created a `0600` age-encrypted local archive with a SHA-256
  sidecar. Restoring it into an isolated Caddy container served the original
  panel and project certificates with matching fingerprints and no new order.
  The operator removed off-host storage from the acceptance contract.

Local commands and the retired-host boundary are recorded in the
[2026-09-24 rehearsal](../../operations/rehearsals/2026-09-24-production-edge-custom-domains.md);
current VDS evidence is in the
[2026-09-26 rehearsal](../../operations/rehearsals/2026-09-26-production-edge-vds.md)
and [2026-10-05 public Caddy rehearsal](../../operations/rehearsals/2026-10-05-public-caddy-control.md).

## Completion and follow-up

All five public Caddy checks above passed on VDS `31.177.109.37`. The TXT
record was visible from authoritative DNS and the selected application
resolver. The VDS default resolvers briefly cached NXDOMAIN; a rehearsal-only
Compose DNS override let the verifier observe the new answer. After the VDS
resolvers caught up, the override was removed; the unmodified production
Compose application still served the verified domain over trusted HTTPS.
The public edge acceptance is complete for the operator's revised
project-domain-only scope. The test used a locally built image because no
published SemVer release image was available to the rehearsal. Testing the
supported `snaphostctl install` path with a published image belongs to
[Task 7](../planned/0007-install-and-upgrade.md); that task also owns the host
disk preflight and minimum-RAM drill. No external-storage drill is required
under the operator's revised installation contract.

See [ADR 0007](../../decisions/0007-custom-domain-tls-edge.md) and the
[project-domain runbook](../../operations/custom-domains.md).
