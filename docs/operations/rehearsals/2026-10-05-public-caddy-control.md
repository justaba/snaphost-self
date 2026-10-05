# Public Caddy HTTPS rehearsal — 2026-10-05

Status: **public control and project HTTPS passed; local encrypted TLS restore passed**

The operator supplied `gorodslv@gmail.com` as both ACME contact and operator
account. The test ran on VDS `31.177.109.37` using a locally built `linux/amd64`
application image and the production Compose manifest. The image was copied to
the VDS under the local test tag `snaphost-rehearsal/snaphost:v0.0.0`; this is
an isolated rehearsal, **not** a published SemVer install via `snaphostctl`.
No OpenRouter key was installed (`LLM_ENABLED=false`).

The host checkout and protected test state are under
`/root/snaphost-edge-rehearsal-20261004`. The running Compose project is
`snaphost-rehearsal`. The operator's bootstrap password was rotated; the new
password is held only in `state/operator-password` with mode `0600`.

## Commands and public DNS

```bash
dig @1.1.1.1 +noall +answer A snaphost.ru
dig @8.8.8.8 +noall +answer A snaphost.ru
dig @1.1.1.1 +noall +answer A kinocassa.ru
dig @8.8.8.8 +noall +answer A kinocassa.ru
dig @1.1.1.1 +noall +answer TXT _snaphost-verify.kinocassa.ru
curl --noproxy '*' -fsS -o /dev/null \
  -w 'status=%{http_code} verify=%{ssl_verify_result}\n' \
  https://snaphost.ru/health
```

Both public resolvers returned `31.177.109.37` for both A records. The TTL
was 3600 seconds at `1.1.1.1`; no AAAA record was observed. The ownership TXT
was initially absent. After the operator added it, the following queries
returned the exact challenge value with TTL 3600:

```bash
dig @1.1.1.1 +noall +answer TXT _snaphost-verify.kinocassa.ru
dig @8.8.8.8 +noall +answer TXT _snaphost-verify.kinocassa.ru
dig @ns1.smartape.ru +noall +answer TXT _snaphost-verify.kinocassa.ru
dig @ns2.smartape.ru +noall +answer TXT _snaphost-verify.kinocassa.ru
```

The VDS's configured recursive resolvers, `188.127.232.11` and
`188.127.232.12`, still returned NXDOMAIN, as did a UDP query from that VDS
to `8.8.8.8`; a UDP query to `1.1.1.1` returned the TXT. The application's
first subsequent sweep therefore stayed `pending/txt_not_found`. A rehearsal
only Compose override set `services.snaphost.dns: [1.1.1.1]`; after application
recreation the next sweep marked `kinocassa.ru` `verified` at
`2026-10-05T05:33:13.895Z`. The shipped production Compose file was not
changed. An operator with the same symptom can wait for negative caches to
expire or select a resolver that sees the authoritative answer.

At `2026-10-05T05:49Z`, both default VDS resolvers also returned a TXT answer.
The temporary override was removed, and the application was recreated from
the unmodified production Compose file (`HostConfig.Dns=null`). The domain
remained `verified`; `ask` returned 200 and public project HTTPS returned 200
with verification result 0 and body B.

The application API attached `kinocassa.ru` to a running static-site deploy.
It returned `pending` and this ownership challenge:

```text
name:  _snaphost-verify.kinocassa.ru
type:  TXT
value: snaphost-verify=fJ7vspkGLekgtIMFv_JyzKO1XkkxeXYa
```

The attached deploy ID was `a94a8320-bde5-40e1-a41a-7ef3de08956d`. Its
container reached `running` after BuildKit built the image from the built-in
static-site template. The domain verifier initially reported `txt_not_found`;
the later public TXT record cleared that state.

## Let’s Encrypt staging

The first Caddy container used
`https://acme-staging-v02.api.letsencrypt.org/directory` with a separate
`state/caddy-staging` directory. It published 80/tcp, 443/tcp and 443/udp.

| Check | Observed result |
| --- | --- |
| `http://snaphost.ru/health` | HTTP 308 |
| `https://snaphost.ru/health` with staging trust bypass | HTTP 200 |
| `GET /tls/ask?domain=kinocassa.ru` from the application container | HTTP 403, pending |
| `GET /tls/ask?domain=unknown.snaphost.ru` | HTTP 403 |
| External unknown and pending SNI handshakes | refused; curl exit 35, HTTP 000 |
| Caddy recreation with the same staging state | identical certificate fingerprint; 1 initial order, 0 new orders |
| `https://kinocassa.ru/` after TXT verification | HTTP 200, staging trust result 20, body `SnapHost deployment A` |
| `http://kinocassa.ru/` | HTTP 308 to HTTPS |

Staging control certificate:

```text
subject: CN=snaphost.ru
issuer: C=US, O=Let's Encrypt, CN=(STAGING) Artificial Amaranth YE1
serial: 2CC66F126DACF5BF426EE6F4162160C32EDE
SAN: DNS:snaphost.ru
notBefore: Oct  4 18:41:14 2026 GMT
notAfter:  Jan  2 18:41:13 2027 GMT
SHA-256: 2B:A4:C5:B1:92:BA:A7:63:68:7B:D6:C7:19:21:92:7C:10:20:32:57:8C:DD:CA:79:C4:AF:A4:DE:69:6F:26:30
```

Staging project certificate, obtained after the internal `ask` returned 200:

```text
subject: CN=kinocassa.ru
issuer: C=US, O=Let's Encrypt, CN=(STAGING) Artificial Amaranth YE1
serial: 2C74E86C863E1654CBA0B0A2B91758885FEC
SAN: DNS:kinocassa.ru
notBefore: Oct  5 04:35:37 2026 GMT
notAfter:  Jan  3 04:35:36 2027 GMT
SHA-256: FE:94:45:D8:89:B6:E1:D5:70:46:8A:1C:9A:C3:6C:AA:CD:02:7A:64:A4:FA:3F:72:36:6E:57:64:43:9B:BA:99
```

## Production Let's Encrypt

Caddy was recreated with the production directory
`https://acme-v02.api.letsencrypt.org/directory` and a fresh `state/caddy`
directory. Staging state was not copied. External `curl` without `-k`
returned `status=200 verify=0` for `https://snaphost.ru/health`, and the panel
root returned HTTP 200. Plain HTTP redirected with 308.

```text
subject: CN=snaphost.ru
issuer: C=US, O=Let's Encrypt, CN=YE2
serial: 0695CF2004A1B74A7092E52797D500886F2A
SAN: DNS:snaphost.ru
notBefore: Oct  5 04:09:12 2026 GMT
notAfter:  Jan  3 04:09:11 2027 GMT
SHA-256: 18:C5:F4:89:24:0A:F1:10:D7:E3:36:2F:C1:53:16:ED:2E:B9:E9:74:E6:18:BB:F0:AA:3C:6C:8E:63:2D:91:AE
```

After a second Caddy recreation, the production certificate fingerprint was
unchanged and the new container had zero `obtaining certificate` log lines.
Unknown and pending SNI still refused the handshake. Only Caddy published
80/443. Docker inspection confirmed that Caddy had a read-only root filesystem
and no Docker socket; the application published only its loopback recovery
port at `127.0.0.1:8080`.

The application login through `https://snaphost.ru` returned HTTP 200; a
subsequent authenticated `/api/v1/auth/me` returned HTTP 200 for
`gorodslv@gmail.com`. The same endpoint returned HTTP 401 anonymously.

The production project URL returned HTTP 200 with TLS verification result 0
and body `SnapHost deployment A`. An unknown SNI was refused at the handshake
(`curl` exit 35, HTTP 000). The production project certificate is:

```text
subject: CN=kinocassa.ru
issuer: C=US, O=Let's Encrypt, CN=YE1
serial: 05FB1D33D2FFA1C79F174A7C5DB871FC9E89
SAN: DNS:kinocassa.ru
notBefore: Oct  5 04:36:28 2026 GMT
notAfter:  Jan  3 04:36:27 2027 GMT
SHA-256: 77:D3:76:EC:D3:71:B2:EB:14:14:22:FB:6A:8D:08:39:7F:CC:5B:B5:3D:D0:F9:02:A8:95:83:90:DF:66:6D:7F
```

## Public alias lifecycle

Commands used the authenticated rehearsal client on the VDS, with its
password read only from the protected host file. External HTTPS checks used
`curl --noproxy '*' -fsS -w '%{http_code} %{ssl_verify_result}'`.

| Action | Public HTTPS result | TLS `ask` | Caddy container ID |
| --- | --- | --- | --- |
| Initial verified alias to A | 200, body `SnapHost deployment A`, verify 0 | 200 | `4ff16a515f07e30753ae104030029bd9ba0c2af4d16f23369cc5d45f8e2ed464` |
| Deploy B with the same `project_key`; automatic promotion | 200, body `SnapHost deployment B`, verify 0 | 200 | same |
| Manual rollback B→A | 200, body `SnapHost deployment A`, verify 0 | 200 | same |
| Manual republish A→B | 200, body `SnapHost deployment B`, verify 0 | 200 | same |
| Stop B while alias targets it | 404, verify 0 | 403 | same |
| Start B from its saved image | 200, body `SnapHost deployment B`, verify 0 | 200 | same |

B is `0e7a2eac-6f76-418c-8d1e-fe0dbe438ec3`. The certificate already in
Caddy can still finish a handshake for a stopped or revoked alias; request
routing denies it. After Caddy was recreated against the same production
state, its ID changed to
`25dd56c96886cd6d1750f0570d24c0d86de6f39b2a0e4b8ffe9be12ee65d6a69`,
while the project certificate fingerprint stayed unchanged and the new
container logged zero `obtaining new certificate` lines.

`DELETE /api/v1/domains/<id>` then returned 204. The next public request was
404 with TLS verification result 0, `ask` returned 403, and the authenticated
domain list was empty. Caddy's ID stayed unchanged. To leave the test domain
working without asking for a fresh ownership TXT, the test database was
restored from an encrypted snapshot taken immediately before detach. The
restored row was `verified` and targeted B. External HTTPS returned 200 with
body B and verification result 0; `ask` returned 200, and Caddy's certificate
fingerprint and container ID remained unchanged. The discarded post-detach
database copy was removed after this check. This restoration is a test-fixture
recovery on an isolated rehearsal installation, not a normal domain attach.

## Encrypted local TLS recovery

No external object store was configured, as requested by the operator.
`age` 1.2.1 was installed on the VDS for this test. A rehearsal age identity
was saved under `state/tls-restore-identity.txt` with mode `0600`; its public
recipient was passed only to `backup.sh tls`. The archive and checksum are in
the protected `backups` directory:

```text
tls-20261005T053809Z.tar.gz.age, 5831 bytes, mode 0600
SHA-256 e32c663ecfd9017d6855f77d53c59fdb554322af480fe2e54d5242774ca4b227
```

The following commands show the recovery procedure; `$base` is the protected
rehearsal directory above, and `$recipient` is produced with
`age-keygen -y "$base/state/tls-restore-identity.txt"`:

```bash
SNAPHOST_BACKUP_AGE_RECIPIENT="$recipient" \
SNAPHOST_TLS_STATE_DIR="$base/state/caddy/data" \
SNAPHOST_BACKUP_DIR="$base/backups" \
bash "$base/infra/backup.sh" tls
(cd "$base/backups" && sha256sum -c tls-20261005T053809Z.tar.gz.age.sha256)
age -d -i "$base/state/tls-restore-identity.txt" \
  "$base/backups/tls-20261005T053809Z.tar.gz.age" \
  | tar -xzf - -C "$base/state/caddy-restore-test"
```

The restored `data` was mounted into a separate container of the same pinned
Caddy image, bound only to `127.0.0.1:8444` and `127.0.0.1:8084`. It served
`kinocassa.ru` and `snaphost.ru` with HTTP 200. Both certificate fingerprints
matched the originals exactly, and its log had zero new-certificate orders.
The isolated SNI checks were:

```bash
echo | openssl s_client -connect 127.0.0.1:8444 -servername kinocassa.ru 2>/dev/null \
  | openssl x509 -noout -fingerprint -sha256 -subject -issuer
curl --noproxy '*' -ksS --resolve kinocassa.ru:8444:127.0.0.1 \
  -o /dev/null -w '%{http_code}\n' https://kinocassa.ru:8444/
docker logs snaphost-caddy-restore-test 2>&1 | grep -c 'obtaining new certificate'
```

They returned the original project fingerprint, HTTP 200 and zero orders.
State directories were `0700`; ACME account and certificate key files were
`0600`. No PEM private-key marker appeared in the encrypted artifact or Caddy
logs. The isolated container and decrypted restore directory were removed
after validation. The encrypted backup and protected identity remain on the
same VDS; this proves restore mechanics but does not protect against total
loss of that machine.

The pre-detach database snapshot was also age-encrypted. Its checksum passed,
the decrypted throwaway SQLite file returned `pragma integrity_check = ok`,
and its `kinocassa.ru` row was `verified` and targeted B. During recovery the
application was stopped, the encrypted SQL was streamed into a new SQLite
file, and that file was swapped into the `snaphost_data` volume before the
application restarted. The restored database was mode `0600`, owner
`1000:1000`. No plaintext test copy was retained.

## Final repository checks

Go checks ran in `golang:1.25-bookworm` and shell suites in `ubuntu:24.04`,
matching their GNU and Bash requirements. The macOS host has no `go` binary
and its native Bash/`stat` cannot run these suites directly.

| Command | Result |
| --- | --- |
| `go test ./...` | passed |
| `go build ./...` and `go vet ./...` | passed |
| `bash infra/tests/deploy_test.sh` | 62 passed, 0 failed |
| `bash infra/tests/snaphostctl_test.sh` | 31 passed, 0 failed |
| `bash infra/tests/backup_test.sh` | 41 passed, 0 failed |
| `bash infra/tests/caddy_integration_test.sh` | live TLS behavior and certificate reuse passed |
| Production `docker compose config --quiet` | passed |
| Pinned Caddy image `caddy validate --config /etc/caddy/Caddyfile` | `Valid configuration` |
| `git diff --check` | passed |

## Remaining acceptance

The public Caddy, project-domain and local encrypted restore checks above
passed. No public SemVer release image was available to this rehearsal for the
supported `snaphostctl install` path; this run used the exact current source
snapshot and a local image tag. The host had about 4.8 GB free, below the
installer's 5 GiB preflight requirement after image pulls. No off-host recovery
was attempted because external storage was removed from the operator's
installation contract. The public edge acceptance belongs to completed
[Task 4](../../tasks/completed/0004-production-edge.md); the supported release
install path belongs to [Task 7](../../tasks/completed/0007-install-and-upgrade.md).
