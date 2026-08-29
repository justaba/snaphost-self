# Custom domains

Status: Deployed and proven end to end on a real domain 2026-08-05
Type: Operations
Updated: 2026-08-05

A customer points their own hostname at us and their deploy answers on it.
The design and the reasoning behind it are in
[ADR 0007](../decisions/0007-custom-domain-tls-edge.md); this is how to run and
diagnose it.

## The path a request takes

```
customer DNS  ──CNAME edge.snaphost.pw / A 135.106.166.76──►  Caddy (VDS :443)
                                                                │ ask: verified?
                                                                ▼
                                                         router-svc (loopback)
                                                                │ GET /internal/routes
                                                                ▼
                                                          user-billing
                                                                │ signed IAM call
                                                                ▼
                                              the user's Yandex Serverless Container
```

Deploys on generated `*.snaphost.pw` hostnames take a **different** path —
Yandex API Gateway to the Yandex-hosted `router-svc` — and never touch the VDS
edge. That separation is deliberate: an edge failure must not take generated
hostnames down with it.

## Attaching a domain (what the user does)

1. `POST /api/v1/domains` with the hostname and project. The response carries a
   TXT challenge and the DNS target.
2. The user creates two records: `_snaphost-verify.<domain>` TXT with the
   token, and either `CNAME → edge.snaphost.pw` (subdomain) or
   `A → 135.106.166.76` (apex, which usually cannot hold a CNAME).
3. The verifier resolves the TXT record on its next pass and flips `pending` →
   `verified`. Nothing routes and **no certificate is issued** before that.
4. The first HTTPS request triggers on-demand issuance, which takes a few
   seconds. Subsequent requests use the cached certificate.

## Host prerequisites

Caddy's configuration is root-owned and is not managed by CD — the deployer
account owns versioned application files and must not be able to change TLS or
proxy policy.

The Caddyfile interpolates `ACME_EMAIL`, `API_GATEWAY_PORT`, and
`ROUTER_BIND_PORT`. The packaged unit has no `EnvironmentFile`, so add one —
without it those expand to empty and the config fails to load:

```bash
cat >/etc/caddy/caddy.env <<'EOF'
ACME_EMAIL=ops@snaphost.ru
API_GATEWAY_PORT=8080
ROUTER_BIND_PORT=8085
EOF
mkdir -p /etc/systemd/system/caddy.service.d
printf '[Service]\nEnvironmentFile=/etc/caddy/caddy.env\n' \
  >/etc/systemd/system/caddy.service.d/override.conf
systemctl daemon-reload
```

Then install the config. **`systemctl reload` does not work here**: `admin off`
removes the admin API, and `caddy reload` talks to it, so a reload fails with
`connection refused` on `localhost:2019` and leaves the running config in
place. A restart is the only way to apply a change, which means a brief gap in
the listener — plan the change accordingly.

```bash
cp -a /etc/caddy/Caddyfile /etc/caddy/Caddyfile.rollback-$(date -u +%Y%m%dT%H%M%SZ)
set -a; . /etc/caddy/caddy.env; set +a
caddy validate --adapter caddyfile --config <new-file>   # validate BEFORE installing
install -o root -g root -m 0644 <new-file> /etc/caddy/Caddyfile
systemctl restart caddy
# verify immediately; restore the rollback copy and restart if anything fails
curl -s -o /dev/null -w '%{http_code}\n' https://api.snaphost.ru/health
```

`caddy validate` does not bind ports or open log files, so it passes on
configurations that still fail at startup. Treat it as a syntax check, not as
proof the config will run.

### Certificate storage

Caddy keeps issued certificates, their private keys, and the ACME account key
under `/var/lib/caddy/.local/share/caddy` (`caddy:caddy`, `0700`, ~148 KB plus
about 4 KB per domain). Losing it means every customer domain re-issues at once
on its next request, against Let's Encrypt limits we do not control — and the
visible symptom is a browser security warning on someone else's published site,
not a 404.

It is backed up by its own timer:

```bash
install -o root -g root -m 0644 \
  <repo>/infra/systemd/snaphost-tls-backup.service \
  <repo>/infra/systemd/snaphost-tls-backup.timer \
  /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now snaphost-tls-backup.timer
```

Separate from the database timer, and running as **root** rather than as the
deployment user, on purpose: the store must stay `caddy:caddy 0700`, and the
deployer account is the identity CD logs in with — letting it read TLS private
keys would widen that blast radius. `backup.sh tls` pipes `tar` straight into
`age`, so the plaintext archive is never a file on disk, and encryption is
mandatory here rather than optional: this artifact is *entirely* key material.

Verify without waiting a day:

```bash
sudo /opt/snaphost/infra/backup.sh --dry-run tls
sudo systemctl start snaphost-tls-backup.service
/opt/snaphost/infra/backup.sh list   # a tls-<UTC>.tar.gz.age appears
```

Restoring, on a rebuilt host — do this **before** starting Caddy, so it finds
its certificates instead of asking for new ones:

```bash
age -d -i snaphost-backup.key -o /tmp/tls.tar.gz tls-<UTC>.tar.gz.age
systemctl stop caddy
tar -xzf /tmp/tls.tar.gz -C /var/lib/caddy/.local/share/
chown -R caddy:caddy /var/lib/caddy/.local
shred -u /tmp/tls.tar.gz
systemctl start caddy
```

## Diagnosing

Work down the path; each step has a distinct symptom.

**Domain stuck in `pending`.** The TXT record is missing or wrong. The reason
code is in `custom_domains.last_error`: `txt_not_found`, `txt_mismatch`, or
`dns_lookup_failed`.

```bash
dig +short TXT _snaphost-verify.<domain>
```

**Browser shows a TLS error / no certificate.** Issuance was refused or
failed. Ask the gate directly, from the host:

```bash
curl -i "http://127.0.0.1:8085/internal/tls/authorize?domain=<domain>"
#   200 → authorised, look further down
#   403 → not a verified domain, or our own suffix
#   503 → router-svc cannot reach user-billing; check that first
```

Then the issuance attempt itself:

```bash
journalctl -u caddy --since '15 min ago' | grep -i "<domain>\|acme\|on-demand"
```

A `403` here with a domain that *is* verified means the two sides disagree
about the hostname — check normalization (trailing dot, case, port).

**Certificate is fine, page is a 404.** TLS worked, routing did not. That is
the router's answer for an unknown, unverified, or revoked host, and also for
a verified domain whose target deploy is not running:

```bash
curl -s "http://127.0.0.1:8085/" -H "Host: <domain>" -o /dev/null -w '%{http_code}\n'
docker logs snaphost-router-svc-1 --since 10m
```

Check the alias actually points somewhere live: `custom_domains.target_deploy_id`
must name a deploy with `status = 'running'` and a non-empty `container_id`.

**Certificate is fine, page is 502/504.** The router reached user-billing but
the upstream container did not answer. Usually a cold start exceeding the
timeout, or a deploy that never listened on `$PORT` — the same failure Task 15b
catches at deploy time.

## Revoking

Detaching a domain (`DELETE /api/v1/domains/:id`) revokes the row immediately,
so the next route lookup and the next `ask` both refuse. The certificate stays
in Caddy's cache until it expires; that is harmless — it can only be presented
for a hostname whose DNS still points at us, and the request behind it gets a
`404`.

If a domain must stop being served *right now*, revoking is enough. There is no
need to touch Caddy.

## Deliberate behaviours worth knowing

- **A verified domain whose deploy is down keeps its certificate.** Dropping it
  would turn a dead page into a browser TLS warning, and re-issuing costs an
  ACME round trip against shared rate limits.
- **The `ask` endpoint is unauthenticated by necessity** — Caddy's `ask` cannot
  send headers. It is safe only because `router-svc` is published on loopback.
  If that port is ever exposed off-box, this endpoint becomes an issuance
  oracle. It also refuses our own suffix outright, so it cannot be used to
  request a duplicate certificate for a generated hostname.
- **Both hops fail closed.** A database error or an unreachable control plane
  answers `503`, never `200`. Caddy retries; a certificate arriving a minute
  late is a smaller problem than one issued for a name nobody proved.
- **Caddy 2.11 removed on-demand rate limiting.** `ask` is the only bound at
  the edge, which puts real weight on `MAX_DOMAINS_PER_USER` and
  `DOMAIN_ATTACH_PER_HOUR`. Treat them as ACME controls, not just product
  limits.

## What is proven, and what is not

Deployed and verified on production 2026-08-04:

- `router-svc` runs as the eleventh Compose service, listening on
  `127.0.0.1:8085` and confirmed **not** reachable off-box;
- the `ask` gate answers correctly on every branch — `403` for an unverified
  hostname, `403` for our own suffix without even consulting billing, `400` for
  a missing domain, `405` for a non-`GET`, and `200` for a row that is
  `verified`. Removing that row returned it to `403` immediately, which is the
  evidence behind the claim that revocation takes effect at once;
- the Caddy config is installed and live, with the control plane still serving.

### End-to-end proof, 2026-08-05

`test.kinocassa.ru` was taken through the whole path against production:

| Step | Result |
| --- | --- |
| Attach through the public API | `pending`, TXT challenge issued |
| TXT + CNAME published in the customer zone | `test.kinocassa.ru` → `edge.snaphost.pw` → `135.106.166.76` |
| Verifier | `txt_mismatch` → `verified` on the next pass |
| First HTTPS request | `200` in **10.5 s** — the ACME issuance |
| Subsequent requests | `200` in ~0.11 s |
| Certificate | `CN=test.kinocassa.ru`, Let's Encrypt, valid to 2026-11-03 |
| `http://` | `301` to HTTPS |
| An unverified host on the same zone | no TLS at all — the `ask` gate refused issuance |

The last row is the one worth keeping: `nope.kinocassa.ru` resolves nowhere and
gets no certificate, so the gate is doing its job under real conditions rather
than only in tests.

The generated hostname for the same deploy kept answering `200` throughout, so
the second ingress did not disturb the first.

### Publishing a new build

A successful deploy repoints every verified domain of its project at itself,
automatically, as the last step of the saga. The previous build serves until
that moment, so a redeploy is invisible from outside and a failed build leaves
the live site alone.

Promotion is best-effort by design: by the time it runs the deploy is live and
paid for, so a failure leaves the domain on the previous build and says so in
the deploy log rather than tearing down a working deploy. `POST
/api/v1/domains/:id/target` remains the manual override, and the same call is
how a rollback is performed.

**Archive deploys need a `project_key` to benefit.** A git deploy derives its
project from the repo URL and branch, so successive builds converge. An
uploaded tarball has no such identity, and without help every upload becomes
its own project — which would mean a domain attached to one could never follow
a later build, and publishing a new version would require detaching,
re-attaching, and asking the customer to edit DNS for a fresh token.

Clients that can persist one value between deploys should generate a key once
and send it every time:

```json
POST /api/v1/deploys
{ "source_type": "archive", "upload_id": "...", "project_key": "my-app" }
```

Anything stable works — a UUID, a hash of the folder path, a name. It is scoped
to the account, namespaced server-side so it cannot collide with a derived git
key, and limited to letters, digits and `- _ . : /`.

The MCP server does not send one yet; until it does, deploys from an AI agent
still land in a fresh project each time.

### Still open

- the MCP server does not send a `project_key` yet, so a domain cannot follow
  successive deploys from an AI agent — see "Publishing a new build" above;
- the off-host copy of both backups — no bucket exists yet, so the encrypted
  archives live only on the machine they protect, which is not yet a backup;
- automatic promotion is built but has not run on production: it ships with the
  release currently blocked on the GitHub Actions outage.

Related: [ADR 0007](../decisions/0007-custom-domain-tls-edge.md),
the operator's own public address,
[Task 16](../inherited/active/0016-custom-domains.md).
