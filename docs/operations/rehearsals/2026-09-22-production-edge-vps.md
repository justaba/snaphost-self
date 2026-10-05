# Production edge: isolated VPS rehearsal

> Historical record: this rehearsal tested the wildcard/Cloudflare and
> external-restore design removed by ADR 0007 on 2026-09-24. Its commands do
> not describe the current production configuration.

Date: 2026-09-22
VPS: `77.95.201.53`
Status: Isolated checks passed; public DNS/ACME and external-store restore not run

The operator confirmed that no domain points to this VPS and no external
backup store is available. The rehearsal therefore used random ports bound to
`127.0.0.1`. It did not occupy public ports 80/443 or claim public certificate
issuance. The remaining acceptance inputs are listed below.

## Host preparation and image

The host ran Ubuntu 24.04 with kernel `6.8.0-139-generic`, Docker `29.1.3`,
Compose `2.40.3+ds1-0ubuntu1~24.04.1` and age `1.1.1`. Docker, Compose and age
were installed for this rehearsal. The test files were copied explicitly to
root-only `/opt/snaphost-edge-test`; no repository environment or secret file
was copied.

The pinned image was built on the VPS itself:

```sh
cd /opt/snaphost-edge-test
docker build \
  -t snaphost-caddy:2.10.2-cloudflare-v0.2.4 \
  -f infra/Dockerfile.caddy infra
docker image inspect snaphost-caddy:2.10.2-cloudflare-v0.2.4 \
  --format 'image={{.Id}} size={{.Size}}'
docker run --rm --entrypoint /usr/bin/caddy \
  snaphost-caddy:2.10.2-cloudflare-v0.2.4 list-modules |
  grep -Fx dns.providers.cloudflare
```

Observed results:

- Caddy version: `v2.10.2`.
- Image: `sha256:13ec582e824fdd794635fce3cafcdf1eb3a6c02bb88da2b068222a3d8b8f9ffd`,
  size 38,598,424 bytes.
- Module output: `dns.providers.cloudflare`.
- Production Compose rendered successfully with protected test values.
- A structural check of the rendered Compose model confirmed that only Caddy
  owns 80/443, Caddy has no Docker socket, and only Caddy receives the DNS
  secret.
- `caddy validate` returned `Valid configuration` with a dummy format-valid
  token while the container used a read-only root filesystem, dropped all
  capabilities except `NET_BIND_SERVICE`, and enabled `no-new-privileges`.
- Starting through the image entrypoint with a malformed token failed before
  Caddy startup, and the token value was absent from the captured log.

The validation token was a non-secret placeholder. No Cloudflare request was
made because the operator has not supplied a zone or token.

## TLS, routing and isolated restore

Command:

```sh
cd /opt/snaphost-edge-test
bash infra/tests/caddy_integration_test.sh
```

The script started the pinned Caddy image and a mock application on a private
Docker network. Its only published port was an ephemeral port on
`127.0.0.1`. The observed behavior was:

| Request or handshake | Result |
| --- | --- |
| `panel.control.test` | HTTPS 200 |
| `one.apps.example.test` | HTTPS 200 and wildcard SAN `*.apps.example.test` |
| unknown generated hostname | HTTPS 404; no individual issuance in the Caddy log |
| verified `ok.custom.test` | HTTPS 200 |
| alias target A → B → A | response changed A → B → A without a Caddy reload |
| detached `ok.custom.test` | HTTPS 404 |
| unknown, pending, revoked and stopped custom names | TLS handshake denied, so no HTTP status exists |

The test then encrypted the actual Caddy `/data` tree with age, copied the
encrypted object and checksum through an isolated S3-compatible command shim,
downloaded them with `restore-tls.sh`, verified the checksum and restored into
an empty directory. This exercises the same download, checksum, decryption,
archive validation and permission code as an external restore, but does not
prove external durability.

Observed restore evidence:

- Encrypted archive SHA-256:
  `49d998f7a2fb864d1261e6b3b1b184b016ea6328de2d2e7653742bf84dd6316e`.
- Restored directory mode: `0700`; the test also required every restored file
  to be `0600` and every restored directory to be `0700`.
- Custom certificate SHA-256 fingerprint before and after restore:
  `63:69:C8:D3:9C:7C:A3:E5:CF:F5:AF:D2:35:23:20:7D:91:B9:08:C2:C0:84:BE:36:64:92:09:3F:DB:B6:23:61`.
- The restored Caddy log contained no new order for `ok.custom.test`.
- No plaintext `.tar.gz` remained, and the age secret key did not occur in the
  backup log.

After cleanup, `docker ps` was empty and `ss` showed no listeners on 80 or 443.
No age identity matching the secret-key format and no plaintext `.tar.gz`
backup remained under the test directory or `/tmp`.
A public resolver returned no PTR answer for `77.95.201.53`. No DNS forward
answers could be tested because no hostname was assigned to the address.

## Missing acceptance evidence

Task 4 remains **In progress**. Completing its public acceptance requires:

1. An operator-owned control hostname, `DOMAIN_SUFFIX` zone and custom test
   domain delegated or pointed to `77.95.201.53`.
2. A Cloudflare API token restricted to the `DOMAIN_SUFFIX` zone with Zone DNS
   Edit and Zone Read permissions, supplied as a root-readable file on the VPS.
3. An S3-compatible off-host bucket and credentials, an age recipient for
   backup, and the corresponding identity kept on a separate recovery host.

With those inputs, repeat the runbook first with the Let's Encrypt staging
directory, then production, and perform the restore after downloading from the
real off-host bucket on the separate recovery host.
