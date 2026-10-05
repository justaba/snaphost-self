# Production edge VDS rehearsal

Date: 2026-09-26
Status: Host and public-socket checks passed; public DNS/ACME evidence remains

This run used the fresh operator-provided VDS `31.177.109.37`. It exercised
the current project-domain-only edge. No production domain pointed to the host,
so the certificate checks below used Caddy's internal issuer and do not count
as Let's Encrypt staging or production evidence.

## Host and trust boundary

- SSH user: `root`, public-key authentication;
- first-seen ED25519 host-key fingerprint:
  `SHA256:b8nEh6Ku42PPpw65idu5iBndkBFPxGsRsEEDKzH9qQg`;
- Ubuntu 26.04, kernel `7.0.0-22-generic`, x86_64;
- 2 vCPU, 1.9 GiB RAM, no swap;
- 9.8 GiB root filesystem, 6.5 GiB free before image pulls;
- public address observed by the host: `31.177.109.37`;
- TCP 80/443 and UDP 443 were free before the rehearsal.

Docker was absent on the fresh host. The rehearsal installed Ubuntu's
`docker.io` 29.1.3 and Docker Compose 2.40.3 packages, then pulled the exact
production Caddy image:

```text
caddy:2.10.2-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d
```

The host was left with Docker installed and enabled, the pulled Caddy and
Python test images, and the non-secret files below
`/root/snaphost-edge-rehearsal-20260926`. No test container or public listener
was left running. After cleanup the two images used 157.9 MB and the root
filesystem had 5.9 GiB free. Local and remote SHA-256 checksums matched for the
Caddyfile, Compose file, environment example and integration test.

## Packaged configuration

The current production Compose file rendered successfully with non-secret
placeholder values. Assertions against its rendered JSON recorded exactly:

```text
public ports: caddy/80-tcp, caddy/443-tcp, caddy/443-udp
caddy Docker socket: absent
caddy image: exact digest above
```

The pinned image validated `infra/Caddyfile.production.example` with a
read-only root filesystem, `no-new-privileges`, all capabilities dropped and
only `CAP_NET_BIND_SERVICE` restored:

```text
Valid configuration
```

## Isolated TLS and route integration

`infra/tests/caddy_integration_test.sh` ran unchanged on the VDS. It verified
the fixed control site, on-demand project-domain site, fail-closed `ask`, alias
changes and persistent `/data` state. Results:

```text
control HTTPS: 200
verified project domain HTTPS: 200
unknown, pending, revoked and stopped SNI: denied
alias: A -> B -> A without Caddy reload
detach: 404
certificate order after Caddy restart: absent
```

The isolated certificate fingerprint was identical before and after restart:

```text
FB:39:3C:E1:7A:86:9D:9A:F1:94:1C:6E:E4:3B:F7:5C:8E:42:BA:B7:23:81:7F:67:88:A0:9E:62:57:6D:5E:8B
```

## External 80/443 path

A second temporary Caddy container published the production port set. Requests
originated outside the VDS and used `--resolve` to send the test SNI directly
to `31.177.109.37`:

| Check | Observed result |
| --- | --- |
| `http://panel.vps.test/` | 308 |
| `https://panel.vps.test/` | 200, body `control` |
| `https://ok.vps.test/` | 200, body `A` |
| `unknown.vps.test` TLS handshake | refused, curl exit 35 |
| alias target changed to B | 200, body `B` |
| alias returned to A | 200, body `A` |
| alias detached | 404 |
| Caddy container during alias changes | unchanged |

The externally observed internal certificate recorded:

```text
issuer: Caddy Local Authority - ECC Intermediate
serial: FC7194FF750CDF5590A2DA525C101762
SAN: DNS:ok.vps.test
notBefore: Sep 25 23:53:52 2026 GMT
notAfter: Sep 26 11:53:52 2026 GMT
SHA-256: 34:1B:25:95:33:C1:7B:FD:07:03:72:C1:78:B7:AC:08:4F:EE:A7:57:C5:CF:98:CA:F0:F3:20:7C:FA:A1:50:08
```

After restarting Caddy against the same state directory, the response remained
`A`, the fingerprint was unchanged and the count of successful orders for
`ok.vps.test` remained `1 -> 1`.

The temporary container had `no-new-privileges`, dropped all capabilities
except `CAP_NET_BIND_SERVICE`, and had no Docker socket. State directories were
mode 0700 and owned by root; no key file was world-readable and no private-key
marker appeared in Caddy logs. Cleanup left zero containers and no listeners on
80/443.

## Remaining public acceptance

The VDS has no operator-owned control or project domain pointing at it. The
following still has to be repeated first with Let's Encrypt staging and then
with production:

1. publish DNS for a control hostname and one project hostname to
   `31.177.109.37`;
2. attach the project hostname and publish its returned ownership TXT record;
3. record public resolver answers, HTTP statuses and the public certificate;
4. repeat the unknown/pending/revoked/stopped and alias lifecycle checks with
   the real application state.

No DNS-provider API token, wildcard certificate or external S3 store is needed
for that run.
