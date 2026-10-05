# Fresh installation and operator account setup

Status: Passed; the production account is left for the operator to create
Type: Operations rehearsal
Updated: 2026-10-06

## Scope and release

The operator explicitly requested an empty installation after backup on
`31.177.109.37`, reusing the control hostname `snaphost.ru`. Existing projects
and the `kinocassa.ru` binding are absent from the new database. Their recovery
data is retained separately. Production uses individual verified custom
domains, with no `DOMAIN_SUFFIX`, DNS-provider token, AI credential or S3
dependency.

- Release: [v0.1.2](https://github.com/justaba/snaphost-self/releases/tag/v0.1.2),
  experimental prerelease, immutable tag.
- Source: `414cb99cf70a9201eee28ddbe5e101c93e214cf4`.
- Anonymous GHCR image: `ghcr.io/justaba/snaphost-self/snaphost:v0.1.2`.
- Pulled digest: `sha256:6f803dc20881d136d270033bea4e9779affafbd2f324e0bda2e37a19371ff0e6`.
- Final VDS audit time: `2026-10-05T21:32:21.035578+00:00`.
- [Checked-release publisher](https://github.com/justaba/snaphost-self/actions/runs/37374776200).

The ordinary main/tag CI attempts encountered GitHub's infrastructure error
`The job was not acquired by Runner of type hosted even after multiple attempts`.
All three source checks succeeded for this exact commit across those attempts.
A separate maintainer recovery workflow on Ubuntu 24.04 verified those results,
verified anonymous registry access and absence of `v0.1.2`, then published the
image using the unchanged tagged Dockerfile. The pending tag publisher was
cancelled first. No existing source tag or image tag was overwritten. Its
workflow syntax passed actionlint 1.7.7, and its source-check/registry gate was
also exercised against the real APIs before dispatch.

## Verified backups before replacement

The backup directory on the host is
`/root/snaphost-reinstall-20261005T203547Z` (0700). Artifacts and checksums are
0600. The original dump restored into temporary SQLite successfully:
`integrity_check=ok`, migration `2|0`, counts `1|8|9|1` for users, projects,
deploys and custom domains.

| Encrypted artifact | SHA-256 |
| --- | --- |
| `database.sql.age` | `a31c19fb0f9ae93cc821b4a9c326cd61fc815b7cb8e9edf22782085a2e0fe92c` |
| `caddy.tar.gz.age` | `e6e71f896c8af3c3f4d873f7e574d78d8ed804946b46622238ccf8b7d924c170` |
| `config-state.tar.gz.age` | `0edd63ea0858c8a2435ead130d66a879eb3035eb973bef89111b3e6479f4c08a` |
| `images.tar.gz.age` | `43a097883cbcd06a4e169b83c538b3ff2337ca5785f8663d123f70a92bb42702` |
| `database-final.sql.age` after services stopped | `f1eab028b458d437d1b3a4afded3223443c66524dd00b85aec5c4a1f11b7caf4` |

The first four artifacts and their `SHA256SUMS` were downloaded to
`/Users/boris/.local/share/snaphost/recovery/20261005T203547Z` (0700).
All checksums matched. In an isolated local container, `age -d` restored
the downloaded SQL into SQLite (`ok`, counts 1/8/9/1), and all three downloaded
archives passed decompression and tar traversal. No decrypted tar or SQL dump
was written to the workstation. The verification database existed only in the
removed container.

The recovery identity is retained separately at
`/Users/boris/.local/share/snaphost/recovery/restore-identity.txt` (0600).
It is also retained in the original protected rehearsal directory on the VDS.
Keys and passwords are omitted from this report.

The 157 MiB image archive was removed from the VDS after verifying its
downloaded copy to meet the installer's unchanged 5 GiB free-space gate. It is
available on the workstation; the original four-file checksum manifest refers
to that complete copy. Unused original and v0.1.0 application images were
removed. No host-wide Docker prune was run.

The old checkout, env and state remain under `prior-install` in the protected
host backup directory. A quiescent database snapshot also remains in Docker
volume `snaphost_reinstall_20261005_prior_data`, outside the fresh Compose
application volume. Stopped old project containers and their images are kept
for recovery and do not serve the new installation.

## Installation commands and TLS restoration

The maintenance script stopped backup timers and the application's project
containers, stopped/removed the Compose service containers while preserving
their networks, copied the quiescent data volume, and
created the final encrypted dump. It included restoration of the prior
checkout, data snapshot and project containers if the install failed.

The new checkout came from the public repository at tag `v0.1.2`. The previous
application and BuildKit cache volumes were replaced. With Caddy stopped, TLS
state was restored by streaming the verified encrypted archive:

```bash
age -d -i "$recovery_identity" "$backup_dir/caddy.tar.gz.age" \
  | tar -xzf - -C /opt/snaphost/state/caddy
chmod 700 /opt/snaphost/state/caddy /opt/snaphost/state/caddy/data
```

The host backup recipient configuration was preserved; the private age
identity is not passed into the application. The installer was then executed:

```bash
SNAPHOST_INSTALL_CONTROL_DOMAIN=snaphost.ru \
SNAPHOST_INSTALL_ACME_EMAIL=gorodslv@gmail.com \
SNAPHOST_INSTALL_PUBLIC_URL=https://snaphost.ru \
  /opt/snaphost/infra/snaphostctl install v0.1.2
```

Installer output is in the protected host `install-v0.1.2.log` (0600), because
it includes the private setup link. The token is a URL fragment, never an HTTP
query parameter, and the panel removes it from the current browser URL. Use
`sudo snaphostctl setup-link` to recover the link while setup is pending.

Two early attempts exercised the recovery path. The first SQL verification
used a read-only copied volume; SQLite needed to create its WAL coordination
files. The second attempt could not read BuildKit's public config because
maintenance `umask 077` had made the checkout files 0600. Both were corrected:
the verifier uses a writable isolated copy and the public BuildKit config is
0644. The fresh clone uses `umask 022`, while runtime env, backup artifacts and
private state retain their protected modes.

The second automatic restoration brought back the old panel and project,
both HTTPS 200, before retrying the installation. Preserving Docker networks
avoids invalidating the old project containers' network references. The final
attempt succeeded, with no account created by the rehearsal.

The final encrypted dump was decrypted into a temporary container database
before removing live data: `ok`, counts 1/8/9/1. It and its checksum were also
downloaded to the workstation recovery directory; checksum matched. Caddy's
three private key files have no group/other permission bits. Caddyfile
validation inside the pinned running image passed. The unused old v0.1.1
image was removed after acceptance checks; its encrypted backup remains on the
workstation and the version remains publicly pullable. Future CI runners are
pinned to Ubuntu 24.04; both workflows passed actionlint.

## Public VDS results

| Check | Result |
| --- | --- |
| `https://snaphost.ru/` | 200, trusted TLS |
| `/login` and `/health` | 200 |
| `GET /api/v1/auth/setup` | 200, `{"required":true}` |
| Setup POST without private token | 403; no account created |
| `/api/v1/auth/me` without session | 401 |
| Prior account login | 401 |
| Fresh DB integrity and schema | `ok`, `2|0` |
| Fresh users/projects/deploys/custom domains | `0|0|0|0` |
| Setup directory/token | 0700/0600, uid:gid 1000:1000 |
| Caddy/app/BuildKit | healthy, zero restarts, no OOM |
| `kinocassa.ru` TLS ask | 403, old ownership binding is absent |
| Old project public request | TLS handshake refused; no old deployment is served |
| Public 80/443 owners | Caddy only |
| Caddy Docker socket | absent |
| New certificate orders after restored state | 0 |
| Token/password/private-key markers in service logs | absent |
| Scheduled database backup | timer active and enabled |

The trusted control certificate's SHA-256 fingerprint remained exactly
`18c5f489240af110d7e3362fc15316ed2eb9e974e618bbf0aa3c6c8e632d91ae`.
Its issuer is Let's Encrypt YE2, SAN `snaphost.ru`, expiration
`Jan  3 04:09:11 2027 GMT`. Caddy served the restored
certificate without another order.

Free space after removing the unused old application image was 5,377,996 KiB
(about 5.13 GiB). The installer's 5 GiB preflight gate passed without an
override. Recovery snapshots and archived checkouts are deliberately retained.

## Implementation checks

- Go build, all tests, vet and gofmt; golangci-lint 1.64.8 built with Go 1.25.
- Auth race test, including concurrent claims against SQLite.
- Panel: 54 tests, ESLint, Prettier, production build.
- Shell suites: deploy 62, backup 41, operator CLI 37; release-tag suite and
  ShellCheck.
- Production Compose validation and pinned Caddy configuration validation.
- Real Caddy routing and restored-state integration in release CI.
- Headless Chrome against the built local panel: fragment removed, chosen
  username/password creates an admin, dashboard opens, normal sign-in after
  logout accepts the same credentials. Isolated browser profile and test
  container removed afterward.
- Real local optional-AI integration: setup with chosen credentials, replay
  409, persistent account after restart, no setup secret/password in service
  logs, template deployment 200, explicit Dockerfile deployment 200,
  unsupported template refused without an AI request or retry loop.

The production setup token was not consumed by test registration. The operator
must choose a login and password of at least 12 characters using the private
link. After setup, deploy projects and attach/verify their domains again.
Retain the recovery copies until the new installation has been accepted.
