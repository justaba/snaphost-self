# Production edge: local implementation proof

> Historical record: this rehearsal tested the wildcard/Cloudflare design
> removed by ADR 0007 on 2026-09-24. Its commands do not describe the current
> production configuration.

Date: 2026-09-21 (US/Pacific)
Status: Local checks passed; public VPS acceptance pending

The checks below ran in the working tree on Docker Desktop. They do not prove
public DNS propagation, Let's Encrypt staging or production issuance, or a
download from the operator's real off-host bucket.

## Commands and observed results

| Command | Observed result |
| --- | --- |
| `docker build -q -t snaphost-caddy:2.10.2-cloudflare-v0.2.4 -f infra/Dockerfile.caddy infra` | Image ID `sha256:e8bd476bb3d850d1115e5004a86b1f4c842e652306db47b160bd8fc0756c2d06`; Caddy v2.10.2 |
| `docker run --rm --entrypoint /usr/bin/caddy snaphost-caddy:2.10.2-cloudflare-v0.2.4 list-modules` | `dns.providers.cloudflare` present |
| `docker compose --env-file <protected test env> -f infra/docker-compose.prod.yml config --quiet` | Exit 0; only Caddy publishes 80/443 and receives `cloudflare_api_token` secret |
| `docker run --rm --entrypoint /usr/bin/caddy ... validate --config /etc/caddy/Caddyfile --adapter caddyfile` | `Valid configuration` for production and staging CA URLs |
| `docker run --rm --read-only --cap-drop ALL --cap-add NET_BIND_SERVICE --security-opt no-new-privileges ... snaphost-caddy:2.10.2-cloudflare-v0.2.4 validate ...` | `Valid configuration` with the protected secret mounted and production container restrictions |
| `bash infra/tests/caddy_integration_test.sh` | Control 200; generated wildcard SAN and unknown generated 404; verified custom 200; unknown/pending/revoked/stopped new SNI denied; alias A→B→A without Caddy reload; detach 404 |
| Same integration script, encrypted restore phase | Real Caddy `/data` encrypted by `age`, copied through a simulated S3 remote, downloaded and checksum verified; restored files 0600 and directories 0700; identical custom certificate SHA-256 fingerprint served after restart; no new order in restored Caddy log; no plaintext tar file |
| `docker run --rm -v "$PWD:/src" -w /src ubuntu:24.04 bash infra/tests/snaphostctl_test.sh` | 28 passed, 0 failed |
| `docker run --rm -v "$PWD:/src" -w /src ubuntu:24.04 bash infra/tests/deploy_test.sh` | 64 passed, 0 failed |
| `docker run --rm -v "$PWD:/src" -w /src ubuntu:24.04 bash infra/tests/backup_test.sh` | 41 passed, 0 failed |
| `docker run --rm -v "$PWD/snaphost-backend:/src" -w /src golang:1.25 sh -c 'go build ./... && go vet ./... && go test ./...'` | All packages passed |

The integration test uses a local certificate for the wildcard and Caddy's
internal issuer for a custom domain. The production Caddyfile is separately
adapted and validated with the pinned Cloudflare-enabled image. The simulated
S3 copy checks the restore logic but is not evidence of external durability.

## Missing public evidence

This machine had no `/opt/snaphost/env/production.env`, backup configuration,
Caddy state, AWS credentials or Cloudflare token in the environment. Its SSH
config had no named hosts. No VPS target, operator-owned DNS zone, scoped
Cloudflare token, external bucket or offline age identity was provided. The
checkout also had no strict SemVer Git tag for this implementation, so
the supported `snaphostctl` production install could not be invoked from it.
There are no real DNS answers, public HTTP statuses, public certificate issuer
or serial data, nor a real external restore result to record. The exact
staging/production and restore commands are in the
[custom-domain](../custom-domains.md#public-acceptance-rehearsal) and
[backup](../backups.md) runbooks. Task 4 remains **In progress**.
