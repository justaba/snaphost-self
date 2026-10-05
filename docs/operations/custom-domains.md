# Project domains

Status: Public control and project-domain ACME verified on 2026-10-05
Type: Operations
Updated: 2026-10-05

Production publishes projects only through explicit verified domains. It does
not allocate generated hostnames and has no `DOMAIN_SUFFIX`, wildcard
certificate or DNS-provider API token.

## Enable attachment

Configure at least one stable public target:

- `DOMAIN_CNAME_TARGET` for subdomains;
- `DOMAIN_A_RECORD_TARGET` for apex domains.

When both are empty, `POST /api/v1/domains` returns 503
`edge_address_unreserved`. `snaphostctl install` sets the control hostname as
the default CNAME target. Set the A-record target to the VPS address if apex
domains are supported.

`RESERVED_DOMAINS` must include the control-plane hostname; the installer adds
it automatically. `MAX_DOMAINS_PER_USER=0` means unlimited and is the self-host
default so every project can have a domain. A positive value applies a global
cap. `DOMAIN_ATTACH_PER_HOUR` still limits attachment attempts.

## Attach and verify

Attach by project:

```json
POST /api/v1/domains
{
  "domain": "app.example.com",
  "project_id": "<uuid>"
}
```

A `deploy_id` may be supplied instead; it selects that deploy as the initial
target and derives its project. Exactly one target form is required.

The response contains the TXT ownership challenge and configured CNAME/A
instructions. Create the returned `_snaphost-verify.<hostname>` TXT record and
point application traffic at the stable edge.

The verifier checks pending rows every `DOMAIN_VERIFY_INTERVAL_SEC`. Verified
rows are periodically rechecked according to `DOMAIN_REVERIFY_HOURS` and the
grace policy. Only a verified domain with a running target is authorized and
routed.

If authoritative DNS returns the TXT but the domain remains
`pending/txt_not_found`, query from the application host too. Its configured
recursive resolvers may still cache NXDOMAIN. The public rehearsal saw the
correct answer at `ns1.smartape.ru`, `ns2.smartape.ru` and `1.1.1.1`, while the
VDS's default resolvers still returned NXDOMAIN. Wait for the negative cache
to expire or give the application a resolver that already sees the record;
do not bypass the ownership check or mark the database row verified by hand.

## Publish and rollback

After a deploy passes its runtime probe, the saga moves every verified domain
of that project to the new deploy. A promotion failure leaves the previous
working target in place.

Manual publish and rollback use the same pointer update:

```json
POST /api/v1/domains/<domain-id>/target
{
  "deploy_id": "<running-deploy-uuid>"
}
```

The deploy must be running and belong to the same project. Archive clients must
reuse a stable `project_key`; otherwise each archive creates another project
and the existing domain cannot follow it.

## Production edge

Production Compose uses the digest-pinned stock Caddy 2.10.2 image and publishes
80/tcp, 443/tcp and 443/udp. It has two narrow internal dependencies:

- `http://snaphost:8082/tls/ask` authorizes issuance only when the exact domain
  resolves to a verified running target;
- `http://snaphost:8081` resolves the request Host and proxies it to the deploy
  container on `snaphost-net`.

The listeners are not published on the host. Caddy receives no Docker socket or
DNS credential. The control-plane hostname is a fixed Caddy site. Project
domains use on-demand HTTP-01 or TLS-ALPN issuance and must already point to the
edge before validation.

Every request resolves SQLite state independently, so alias promotion,
rollback and detach need no Caddy reload. Unknown, pending, revoked, stopped and
targetless domains fail closed. A cached certificate may still complete TLS
after revocation, but routing answers 404.

## Public acceptance rehearsal

Use operator-owned control and project domains. For the first run, set
`SNAPHOST_INSTALL_ACME_CA=https://acme-staging-v02.api.letsencrypt.org/directory`
and use an isolated staging Caddy state directory. Check DNS externally:

```bash
dig @1.1.1.1 +short A panel.example.org
dig @1.1.1.1 +short A project.example.com
dig @1.1.1.1 +short TXT _snaphost-verify.project.example.com
```

After the domain is verified and targets a running deploy, record HTTP results
and certificate details:

```bash
curl --noproxy '*' -ksS -o /dev/null -w '%{http_code}\n' https://panel.example.org/health
curl --noproxy '*' -ksS -o /dev/null -w '%{http_code}\n' https://project.example.com/
echo | openssl s_client -connect project.example.com:443 -servername project.example.com 2>/dev/null \
  | openssl x509 -noout -subject -issuer -serial -dates -ext subjectAltName
sudo docker compose --project-name snaphost \
  --env-file /opt/snaphost/env/production.env \
  -f /opt/snaphost/infra/docker-compose.prod.yml logs --no-color caddy
```

Exercise unknown, pending, revoked and stopped domain handshakes. Promote the
verified alias to a new deploy, return it to the prior deploy, then detach it.
Record response changes and the unchanged Caddy container ID.

After staging succeeds, use a fresh `/opt/snaphost/state/caddy` directory,
switch `SNAPHOST_ACME_CA` to the production Let's Encrypt directory and repeat
without `curl -k`. Do not copy staging state into production.

`snaphost.ru` and `kinocassa.ru` point to VDS `31.177.109.37`. Their public
staging and production certificates, live HTTPS routing, alias promotion and
rollback, stopped and detached denial, and local encrypted Caddy-state restore
passed; see the [2026-10-05 rehearsal](rehearsals/2026-10-05-public-caddy-control.md).
External S3-compatible TLS backup is not part of this installation contract.

## Detach

`DELETE /api/v1/domains/<id>` revokes the row. New certificate authorization
fails and requests stop routing. Existing certificate material in Caddy state
does not restore the revoked route.

See [ADR 0007](../decisions/0007-custom-domain-tls-edge.md) and
[completed Task 4](../tasks/completed/0004-production-edge.md).
