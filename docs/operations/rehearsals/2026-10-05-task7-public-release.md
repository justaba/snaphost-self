# Task 7 public release acceptance — 2026-10-05

Status: **Passed**

The public GitHub/GHCR installation path, upgrade, guarded rollback and
encrypted local database restore passed on `31.177.109.37`. A separate
1 GiB amd64 Linux guest installed the published image anonymously and built
the checked-in React/Vite application while health and an existing site stayed
available. These results close [Task 7](../../tasks/completed/0007-install-and-upgrade.md).

## Published releases and CI

Both immutable Git tags are on `main` and have experimental GitHub
prereleases. Prerelease status describes product maturity; the operator CLI
accepts their strict SemVer tags.

| Version | Git commit / OCI revision | Public image digest |
| --- | --- | --- |
| [v0.1.0](https://github.com/justaba/snaphost-self/releases/tag/v0.1.0) | `2ccf9453a1e28a2bc22aeba59fe76bc5ec3ddc40` | `sha256:1c87ae02d7efbbef4f99adb4b4b723d44684fa3803c1b41a451fc422726301b2` |
| [v0.1.1](https://github.com/justaba/snaphost-self/releases/tag/v0.1.1) | `ccdbc6d797fcf12e8ae545e94e6972c996471286` | `sha256:4c436a3e5026b46ffe52390dd59016a00fd4f148ec4c9d103154a0d5ed035f65` |

The image prefix is `ghcr.io/justaba/snaphost-self/snaphost`. Anonymous
manifest requests returned HTTP 200. On the VDS, both `docker pull` commands
passed using an empty `DOCKER_CONFIG`; the pulled OCI revisions matched
`git rev-parse "$VERSION^{commit}"`. No registry credential was installed.

Publication used the release procedure, after the user authorized publishing:

```bash
git push origin main
git tag -a v0.1.0 2ccf9453a1e28a2bc22aeba59fe76bc5ec3ddc40 -m 'SnapHost v0.1.0'
git tag -a v0.1.1 ccdbc6d797fcf12e8ae545e94e6972c996471286 -m 'SnapHost v0.1.1'
git push origin v0.1.0 v0.1.1
```

Go, panel, deployment scripts and image publishing passed in the
[main workflow](https://github.com/justaba/snaphost-self/actions/runs/37322667805),
[v0.1.0 workflow](https://github.com/justaba/snaphost-self/actions/runs/37323756136)
and [v0.1.1 workflow](https://github.com/justaba/snaphost-self/actions/runs/37323755956).
The checks include Go tests/vet/golangci-lint, 47 panel tests, ESLint,
Prettier, panel build, deploy 62 / backup 41 / operator 35 scenarios,
release-tag tests, ShellCheck, production Compose rendering, pinned Caddyfile
validation and actual Caddy routing/TLS/state-reuse integration.

## VDS preparation and fresh install

- Ubuntu 26.04 LTS, amd64, 2 vCPU, `MemTotal=2009568 KiB`, swap 0;
- the root filesystem is 9.8 GiB; free space before installation exceeded
  the default 5 GiB preflight gate;
- control hostname `snaphost.ru`, verified project hostname `kinocassa.ru`;
- operator and ACME contact `gorodslv@gmail.com`;
- AI disabled; no generated production suffix, DNS API token or S3 required.

The earlier locally built edge rehearsal occupied 80/443. Before the handoff,
its database, env and Caddy state were preserved. The old Compose project was
stopped without deleting its volumes. Only stopped temporary Node/test images,
unused BuildKit cache, apt cache and archived journal segments were reclaimed.
Archived journals were encrypted and checked before rotation/vacuuming.
Original project images, database, protected config and TLS state were retained.

The protected advanced env for the first release contained the three required
settings plus the operator email, `BUILDKIT_MEMORY_LIMIT=768M` and
`SNAPHOST_PUBLIC_SMOKE_URL=https://snaphost.ru`. The explicit memory limit is
necessary for this `v0.1.0` baseline, which predates automatic RAM sizing.
Config and evidence logs stayed mode 0600; secrets were not copied into this
report.

```bash
git clone --branch v0.1.0 https://github.com/justaba/snaphost-self.git /opt/snaphost
# Prepare /opt/snaphost/env/production.env with mode 0600.
# Restore the protected prior Caddy data into /opt/snaphost/state/caddy.
/opt/snaphost/infra/snaphostctl install v0.1.0
systemctl is-active snaphost-backup.timer
systemctl start snaphost-backup.service
```

The first attempt supplied a smoke URL ending in `/health`. The deploy engine
appends `/health`, so `/health/health` answered 401 after migrations. The
engine recorded manual intervention instead of falsely reporting success.
The prior stack was restored and its A/B deployments restarted, both public
endpoints answered 200, and the URL was corrected to its base origin. Only the
new empty failed installation's volumes were reset before repeating install;
the preserved prior installation was untouched. This was an incorrect
rehearsal input, not an automatic retry across migrations.

Successful fresh install completed at **14:33:20 UTC**. SQLite migrated from
empty state to schema 2, all three services became healthy, the database timer
was active, and `/usr/local/sbin/snaphostctl` matched the installed release.
Fresh login and `/auth/me` returned 200; anonymous `/auth/me` returned 401.
Password change returned 204, the old bootstrap password then returned 401,
and the new password authenticated with 200. Passwords were read/written only
in protected files and HTTP request bodies, never command-line arguments.

After proving the fresh account, the preserved prior SQL dump was streamed
through age into the new volume with the app stopped. This retained the
operator's existing account, projects and verified domain. A and B were
started through the API. The public domain again served deployment B with
HTTP 200. Existing public certificates were reused; fresh public issuance was
already proved by the [Task 4 rehearsal](2026-10-05-public-caddy-control.md).

## Upgrade, rollback and final version

Both releases contain the same Go/panel source and SQLite migration set.
Schema was `2`, `dirty=0`, and `integrity_check=ok` at every checkpoint.

```bash
snaphostctl upgrade v0.1.1
snaphostctl rollback                 # expected refusal without compatibility confirmation
MIGRATIONS_BACKWARD_COMPATIBLE=true snaphostctl rollback --dry-run
MIGRATIONS_BACKWARD_COMPATIBLE=true snaphostctl rollback
snaphostctl upgrade v0.1.1
```

| Operation | Completed UTC | Result |
| --- | --- | --- |
| Install `v0.1.0` | 14:33:20 | exact Git tag, env/state/image aligned; fresh login and rotation passed |
| Upgrade `v0.1.1` | 14:35:36 | exact tag and OCI revision; protected explicit 768 MiB build limit retained |
| Rollback without compatibility flag | before 14:38:20 | refused: `rollback blocked: migrations ran and backward compatibility is not confirmed` |
| Compatibility-confirmed dry-run | before 14:38:20 | no file, Git HEAD or container change |
| Rollback `v0.1.0` | 14:38:20 | checkout, CLI, runtime and state coordinated; account/projects retained |
| Return to `v0.1.1` | 14:39:55 | exact tag/OCI revision; all services healthy |

Dry-run checksums covered `production.env`, current/previous state, the stable
CLI, AppArmor profile and all four installed backup units. Git HEAD and the
three Compose container IDs were identical before/after. Installed units also
passed `systemd-analyze verify` on the actual host.

At baseline, upgrade, rollback and final restore, the authenticated health,
`/api/v1/auth/me`, projects and domains returned 200, anonymous `/auth/me`
returned 401, and the control/project public endpoints returned 200 with a
trusted TLS chain. Certificate SHA-256 fingerprints remained:

```text
snaphost.ru  18c5f489240af110d7e3362fc15316ed2eb9e974e618bbf0aa3c6c8e632d91ae
kinocassa.ru 77d376ecd371b2eb141422fb6a8d08397fcc5bb53dd0f902a8958390df666d7f
```

The final [sanitized audit](2026-10-05-task7-public-audit.json) records the
certificate subjects, issuers, validity, ports, health, revisions and restore
checks. Actual Docker inspection confirmed that only Caddy publishes 80/443,
its root filesystem is read-only and it has no Docker socket. The app recovery
port is bound to `127.0.0.1:8080`; its routing/ask listeners are private.

## Installed encrypted backup and live restore

At **14:46:55 UTC**, the installed `snaphost-backup.service` produced
`scheduled-20261005T144655Z.bGA8fv.sql.age`, **69709 bytes**, mode **0600**:

```text
SHA-256 ade4497c4a9abd3b21ba0a624d77ed9f4b938e1b491deb8bffb44692d6a03269
```

`last-backup.env` recorded `encrypted=true`, `offhost=false`; `sha256sum -c`
returned `OK`. Only the public age recipient is in protected `env/backup.env`.
The identity is a separate mode-0600 root file; the backup service does not
read it. No remote storage was configured, as requested by the operator.

The actual restore used these root-shell commands (the filename identifies
the tested artifact; select the desired backup for another recovery):

```bash
set -euo pipefail
umask 077
compose=(docker compose -p snaphost \
  --env-file /opt/snaphost/env/production.env \
  -f /opt/snaphost/infra/docker-compose.prod.yml)
# Take a final recovery backup while the app is still running.
systemctl start snaphost-backup.service
artifact=/opt/snaphost/backups/scheduled-20261005T144655Z.bGA8fv.sql.age
identity=/root/snaphost-edge-rehearsal-20261004/state/tls-restore-identity.txt
cd /opt/snaphost/backups
sha256sum -c "$artifact.sha256"
app=$("${compose[@]}" ps -q snaphost)
volume=$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/var/snaphost/data"}}{{.Name}}{{end}}{{end}}' "$app")
image=$(docker inspect --format '{{.Config.Image}}' "$app")
"${compose[@]}" stop snaphost
age -d -i "$identity" "$artifact" | docker run --rm -i \
  --network none --user 1000:1000 -v "$volume:/data" \
  --entrypoint sqlite3 "$image" /data/restore.db
test "$(docker run --rm --network none --user 1000:1000 \
  -v "$volume:/data" --entrypoint sqlite3 "$image" \
  /data/restore.db 'pragma integrity_check;')" = ok
docker run --rm --network none --user 1000:1000 -v "$volume:/data" \
  --entrypoint sh "$image" -c \
  'chmod 600 /data/restore.db; rm -f /data/snaphost.db-wal /data/snaphost.db-shm; mv /data/restore.db /data/snaphost.db'
"${compose[@]}" start snaphost
```

The drill compared schema and durable row counts before/after: **1 user,
8 projects, 9 deploys, 1 custom domain**, schema `2|0`, integrity `ok`.
Live DB permissions were **0600 / 1000:1000**. At **14:46:58 UTC**, login,
authenticated API reads, control HTTPS and project B HTTPS passed again.
Caddy's container ID was unchanged, with **zero new certificate orders** and
no private-key markers in its log. The temporary restore DB was atomically
renamed, leaving no extra decrypted SQL file.

Deploy's pre-migration recovery SQL files are deliberately separate local
mode-0600 artifacts; they were retained for rollback recovery. Encryption of
scheduled backups does not imply that these protected recovery files or the
live database are encrypted. Keeping archive and identity on this VDS proves
restoration mechanics and does not protect against losing the whole host.

## Public image on the minimum amd64 host

The [Lima configuration](../../../infra/tests/rehearsal-1g-amd64.lima.yaml)
pins the Ubuntu 24.04 amd64 cloud image by SHA-256, with 2 vCPU, 1 GiB RAM,
20 GiB disk, no host mounts and no forwarded TCP/UDP ports. On this Apple
Silicon workstation it used Lima 2.2.1/QEMU 11.1.2 emulation. Build duration
is an emulation measurement, not a VPS speed benchmark.

```bash
limactl start --name=snaphost-task7-amd64-1g infra/tests/rehearsal-1g-amd64.lima.yaml
limactl shell snaphost-task7-amd64-1g
sudo git -c credential.helper= clone --branch v0.1.1 \
  https://github.com/justaba/snaphost-self.git /opt/snaphost
# Prepare protected production.env with the required version/domain/email,
# ACME staging and a loopback-only HTTP test session (SESSION_COOKIE_SECURE=false).
sudo /opt/snaphost/infra/snaphostctl install v0.1.1
# Sign in and rotate the generated password; store it in the protected file.
sudo python3 /opt/snaphost/infra/tests/node_build_rehearsal.py \
  --email operator@task7.invalid \
  --password-file /opt/snaphost/state/operator-password \
  --output /opt/snaphost/state/node-public-amd64-1g.json
```

Docker 29.1.3 / Compose 2.40.3 pulled the public `v0.1.1` image without any
GHCR login; `/root/.docker/config.json` did not exist. Install completed at
14:30:40 UTC and derived `BUILDKIT_MEMORY_LIMIT=448M` from actual Linux
`MemTotal=978712 KiB`. Login/password rotation, health, AppArmor and the
installed backup timer passed. Invalid `.invalid` test DNS names did not
request trusted public certificates; those were verified on the VDS.

The fixture uses React/React DOM 19.2.4, Vite 8.0.4, `npm ci`, a committed
lockfile and digest-pinned Node 22 Alpine. The
[measurement JSON](2026-10-05-task7-public-amd64-1g.json) contains 220 samples:

| Measure | Result |
| --- | ---: |
| Created → running | 14:32:58.719 → 14:33:53.105 UTC; **54.4 s** |
| Total including polling and asset checks | 57.1 s |
| Host RAM in use peak | **661.6 MiB** |
| Host swap / peak swap used | **0 / 0** |
| BuildKit limit / peak | **448 / 351.7 MiB** |
| App / Caddy / existing site peak | 105.8 / 52.7 / 10.5 MiB |
| OOM counter deltas / restarts | **0 / 0** |
| Successful health and existing-site probe pairs | **33** |
| Node HTML and compiled React JavaScript | **HTTP 200** |

Both temporary deployments were stopped. The isolation rule was corrected to
match wildcard UDP as well as TCP bindings, then validated and restarted;
`lsof` confirmed Lima no longer listened on the host's UDP 443. The guest was
stopped after capturing evidence.

## Final repository checks

Before recording acceptance, Go build/vet/tests/gofmt passed in the Go 1.25
container. The frozen pnpm 11.0.9 install, 47 Vitest tests, ESLint, Prettier
and panel production build passed in Node 22. In Debian with GNU tools and
Bash 5, the deploy/backup/operator suites passed **62/41/35** scenarios and
the release-tag suite and ShellCheck passed. The suites require this Linux
tooling; macOS's bundled Bash 3 and Alpine's different utility paths do not
match their supported environment.

Production Compose and the real Caddyfile were checked without reading a
developer's env file:

```bash
SNAPHOST_VERSION=v0.1.1 \
SNAPHOST_CONTROL_DOMAIN=panel.example.invalid \
SNAPHOST_ACME_EMAIL=operator@example.invalid \
  docker compose --env-file /dev/null -f infra/docker-compose.prod.yml config --quiet
docker run --rm \
  -e SNAPHOST_CONTROL_DOMAIN=panel.example.invalid \
  -e SNAPHOST_ACME_EMAIL=operator@example.invalid \
  -v "$PWD/infra/Caddyfile.production.example:/etc/caddy/Caddyfile:ro" \
  caddy:2.10.2-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d \
  caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
bash infra/tests/caddy_integration_test.sh
limactl validate infra/tests/rehearsal-1g-amd64.lima.yaml
git diff --check
```

Compose returned 0, Caddy returned `Valid configuration`, and real handshakes
proved control/project 200, alias B/A, detach 404, denied SNI and certificate
reuse after restart with no new order. Test containers/network/TLS volume
were removed. Lima validation passed; both owned rehearsal VMs are stopped.

## Scope and remaining limitations

The accepted path is one documented Ubuntu/systemd/AppArmor/Compose install,
with public **amd64** images. This is an experimental preview platform:
preview TTL still applies, project volumes and managed databases remain future
tasks. Large Node dependency graphs can exceed the measured build budget;
the earlier [Vite 6 pressure failures](2026-10-05-task7-vds-node.md#react-pressure-probe)
remain relevant. No public arm64 release is claimed.

On the small VDS, final free disk space was only about 5.05 GiB, just above
the 5 GiB preflight gate. Use the runbook's recommended 20 GiB disk and size
storage for actual images/cache. Backup encryption and local restore passed;
off-host disaster recovery remains an optional operator policy. The original
rehearsal state and protected recovery backups remain available on the VDS.
