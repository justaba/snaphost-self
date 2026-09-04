# Custom domains

Status: Compose edge implemented; public DNS/ACME proof remains
Type: Operations
Updated: 2026-09-04

The application can register a domain, prove ownership through DNS, keep its
verification state, point it at a running deploy and route it through the
Compose-managed Caddy edge. The public path has not yet been accepted against
real operator-owned DNS and ACME.

## Enable attachment

At least one stable public target must be configured:

- DOMAIN_CNAME_TARGET for subdomains;
- DOMAIN_A_RECORD_TARGET for apex domains.

When both are empty, POST /api/v1/domains returns 503
edge_address_unreserved. This is the default local-development behavior because
the repository does not reserve a public address for the operator.

RESERVED_DOMAINS must include the control-plane domain and DOMAIN_SUFFIX is
always reserved implicitly. MAX_DOMAINS_PER_USER and
DOMAIN_ATTACH_PER_HOUR bound attachment attempts. Do not enable
DOMAIN_ATTACH_REQUIRE_IDENTITY: no payment or external identity signal exists
in this self-hosted fork, so enabling it intentionally refuses every attach.

## Attach and verify

Attach by project:

~~~json
POST /api/v1/domains
{
  "domain": "app.example.com",
  "project_id": "<uuid>"
}
~~~

A deploy_id may be supplied instead; it selects that deploy as the initial
target and derives its project. Exactly one target form is required.

The response includes the TXT challenge and configured CNAME/A instructions.
Create the returned _snaphost-verify.<hostname> TXT record and point application
traffic at the configured stable edge.

The in-process verifier checks pending rows every
DOMAIN_VERIFY_INTERVAL_SEC. Verified rows are periodically rechecked according
to DOMAIN_REVERIFY_HOURS and the grace policy. Only verified domains with a
running target are authorized and routed by the edge.

List the row through GET /api/v1/domains to see status and last_error. Common
verification errors are txt_not_found, txt_mismatch and dns_lookup_failed.

## Publish and rollback

After a deploy passes its runtime probe, the saga best-effort moves every
verified domain of that project to the new deploy. A promotion failure leaves
the domain on the prior working deploy and logs a warning.

Manual publish or rollback is the same atomic pointer update:

~~~json
POST /api/v1/domains/<domain-id>/target
{
  "deploy_id": "<running-deploy-uuid>"
}
~~~

The target must be a running deploy in the same project.

Archive clients must reuse a stable project_key when creating deploys. Without
it, every archive is a new project and an existing domain cannot follow the new
build.

## Production edge

`infra/docker-compose.prod.yml` runs the digest-pinned Caddy 2.10.2 image and
publishes 80/tcp, 443/tcp and 443/udp. No other production service owns those
ports. `infra/Caddyfile.production.example` uses two narrow internal
dependencies:

- `http://snaphost:8082/tls/ask` authorizes on-demand issuance only when the exact
  hostname resolves to a running deploy;
- `http://snaphost:8081` resolves the request Host and proxies it to the deploy
  container on `snaphost-net`.

Those application listeners are not published on the host. They are not
registered below `/api` or `/internal`, and Caddy receives no Docker socket. An
alias target move takes effect on the next request because the proxy reads
SQLite for every request.

`snaphostctl install` requires `SNAPHOST_INSTALL_CONTROL_DOMAIN` and an ACME
email, stores them in the protected production env, reserves the control
hostname from attachment, sets it as the default CNAME traffic target and
creates `/opt/snaphost/state/caddy/{data,config}` with mode 0700. Keep the
application recovery bind at its default `127.0.0.1:8080`.

The remaining boundary is public acceptance: no generated-host wildcard/DNS
challenge contract is packaged, and real public DNS, ACME issue, promotion,
rollback and detach still need a VPS rehearsal. The current catch-all uses
on-demand certificates and its fail-closed `ask` endpoint; issuing one
certificate per preview can encounter CA rate limits.

Before starting the stack, point the control hostname at the VPS and configure
wildcard DNS for `DOMAIN_SUFFIX`. Custom subdomains normally CNAME to the
control hostname; apex domains need `DOMAIN_A_RECORD_TARGET` set to the VPS
address. Ensure the firewall admits TCP 80/443 and UDP 443, and that no host
web server is already bound there.

## Detach

DELETE /api/v1/domains/<id> revokes the row. New requests stop routing and new
certificate authorization fails closed. A certificate already cached by Caddy
remains in edge storage until its own lifecycle removes it; possession of that
certificate does not restore a revoked route.

See [ADR 0007](../decisions/0007-custom-domain-tls-edge.md) and
[deployment model](../architecture/deployment-model.md). Remaining acceptance
work is tracked in [Task 4](../tasks/planned/0004-production-edge.md).
