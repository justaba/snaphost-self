# Install and upgrade

Status: Public install, upgrade, rollback and encrypted local restore accepted
Type: Operations
Updated: 2026-10-05

This is the supported release layout for one self-hosted machine. The operator
owns one Git checkout at `/opt/snaphost`; its checked-out tag, the application
image, `env/production.env` and `state/current.env` must all name the same exact
release. `snaphostctl` enforces that invariant before it changes the host.

The installer brings up the control plane, BuildKit and a digest-pinned stock
Caddy image. Caddy is the only component that publishes 80/443; it reaches the
application routing and TLS authorization listeners only through the Compose
control network. A temporary plain-HTTP panel is possible only through an
explicit, insecure opt-in described below.

The [2026-10-05 public-release rehearsal](rehearsals/2026-10-05-task7-public-release.md)
passed anonymous install of `v0.1.0`, upgrade to `v0.1.1`, guarded rollback,
password rotation and encrypted local SQLite backup/live restore. The panel
and verified project retained trusted HTTPS and their certificate fingerprints.
The earlier [public edge rehearsal](rehearsals/2026-10-05-public-caddy-control.md)
proved staging/production issuance and isolated TLS-state restoration.

## Host prerequisites

- a systemd Linux host with AppArmor and `apparmor_parser`; the supplied
  rootless-BuildKit profile targets Ubuntu 24.04;
- amd64, 2 vCPU and 1 GiB RAM for the measured small fixture; allocate more
  RAM for larger builds and concurrent sites;
- 20 GiB disk recommended; preflight requires at least 5 GiB free on the
  filesystem that holds the installation;
- Docker Engine 24 or newer, Docker Compose v2, and a running Docker daemon;
- Git, GNU coreutils, `awk`, `sed`, `grep`, `flock`, `curl` and `ss` from
  iproute2;
- root access for the install, AppArmor profile and systemd timer;
- outbound HTTPS to GitHub, Docker Hub, the public GHCR package and ACME
  endpoints;
- TCP ports 80/443 and UDP port 443 free for Compose Caddy;
- operator-controlled DNS for the panel and every project domain.

The [public amd64-image drill](rehearsals/2026-10-05-task7-public-release.md#public-image-on-the-minimum-amd64-host)
built the small React/Vite fixture on 1 GiB RAM with no swap, a derived
448 MiB BuildKit limit and 661.6 MiB peak host RAM in use. Health and an
existing site stayed available, with no OOM or restart. QEMU-emulated build
time was 54.4 seconds. Published CI images target amd64; an earlier arm64
source-build drill is additional evidence, not a public arm64 release.
The older [Vite 6 workload](rehearsals/2026-10-05-task7-vds-node.md#react-pressure-probe)
exceeded 768 MiB and 1 GiB build limits. Size the host for actual projects;
these measurements do not guarantee that arbitrary Node builds fit on 1 GiB.

The GHCR package must be public. If an install asks for `docker login`, package
visibility is wrong; a registry credential is not part of this contract.

## First install

Choose a published strict SemVer tag. Do not install `main`, `latest`, or a Git
SHA.

~~~bash
VERSION=v0.1.2
sudo git clone https://github.com/justaba/snaphost-self.git /opt/snaphost
sudo git -C /opt/snaphost checkout --detach "$VERSION"
~~~

For a non-interactive install behind an HTTPS edge:

~~~bash
sudo env \
  SNAPHOST_INSTALL_CONTROL_DOMAIN=panel.example.org \
  SNAPHOST_INSTALL_ACME_EMAIL=acme@example.org \
  SNAPHOST_INSTALL_PUBLIC_URL=https://panel.example.org \
  /opt/snaphost/infra/snaphostctl install "$VERSION"
~~~

When the operator email is set, `SNAPHOST_INSTALL_ACME_EMAIL` may be omitted to
reuse it. The public smoke URL must be the base origin (for example,
`https://panel.example.org`), without `/health`: the deploy appends that path.
`SNAPHOST_INSTALL_OPERATOR_EMAIL` is a legacy ACME contact default, not the
login identifier. Choose your login and password later in the browser.
Omit `SNAPHOST_INSTALL_PUBLIC_URL` when HTTPS is not ready; the deploy then performs
only its internal readiness check while Caddy waits for DNS. An interactive
terminal may omit the control-domain and ACME-email variables and answer the
prompts instead. No provider key or AI prompt is required by default.
For a first public rehearsal, set
`SNAPHOST_INSTALL_ACME_CA=https://acme-staging-v02.api.letsencrypt.org/directory`
and `SNAPHOST_CADDY_STATE_DIR=/opt/snaphost/state/caddy-staging` in the
installer environment. The [custom-domain runbook](custom-domains.md#public-acceptance-rehearsal)
describes the switch to fresh production state after staging succeeds.

The command:

1. refuses a dirty checkout or a checkout not at the requested tag;
2. refuses a fresh install while TCP 80/443 or UDP 443 is already occupied,
   then writes `/opt/snaphost/env/production.env` with mode `0600`;
3. derives the numeric group of `/var/run/docker.sock`, a BuildKit CPU limit
   that does not exceed the host CPU count, and an initial BuildKit memory limit
   of half host RAM rounded down to 64 MiB (capped at 4 GiB);
4. installs and reloads the BuildKit AppArmor profile;
5. creates protected persistent Caddy state, pulls the digest-pinned stock
   Caddy 2.10.2 image and exact application version, migrates
   SQLite and waits for snaphost and Caddy health;
6. installs the database and optional TLS backup units and starts only
   `snaphost-backup.timer`;
7. installs the stable command as `/usr/local/sbin/snaphostctl`;
8. prints a private one-time setup link for choosing your login and password.

The installed BuildKit daemon manages its cache with three explicit targets:
it retains at least 512 MB, starts broader reclamation above 4 GB, and tries to
leave 5 GB free on the host filesystem. GC is periodic, so this is not a hard
quota during an active build. Every deploy and rollback recreates BuildKit; on
deploy that happens before migrations, so a policy change in the checked-out
release takes effect instead of waiting for a host reboot.
`BUILDKIT_MEMORY_LIMIT` in the protected env file overrides the initial
host-sized value when a measured workload needs a different limit. Check RAM,
swap and the memory use of the control plane and running sites before raising
it. The limit is calculated from Linux's actual `MemTotal`, which is slightly
below the advertised RAM of a VPS. This sizing rule does not prove that
arbitrary Node builds succeed on 1 GiB.

Open the private setup link, choose a login (name or email), and enter your own
password twice. The panel signs you in and permanently closes setup. The
chosen password is never printed in logs. `sudo snaphostctl setup-link`
retrieves the pending link if needed. Existing accounts keep their credentials
on upgrade; see [operator setup](operator-setup.md).

If the first deploy fails before migrations, running the same install command
again resumes from the protected env file. If `state/in-progress.env` says
migrations started or manual intervention is required, inspect it instead of
deleting it.

### Advanced configuration

To set optional values before first start, copy
`infra/.env.production.example` to `/opt/snaphost/env/production.env`, edit it,
and keep it mode `0600`. Set the requested exact version and all three required
values. `snaphostctl install` recognizes that protected file as an incomplete
install, derives `DOCKER_SOCKET_GID`, and continues without overwriting it.

AI Dockerfile generation is disabled by default. Project Dockerfiles and
built-in templates work without a key; other projects must supply a Dockerfile.
To enable the optional fallback on first install, prepare a protected key file:

~~~bash
sudo install -m 600 /dev/null /root/snaphost-openrouter.key
sudoedit /root/snaphost-openrouter.key
~~~

Put only the provider key in it, with no shell assignment. Add both
`SNAPHOST_INSTALL_LLM_ENABLED=true` and
`SNAPHOST_INSTALL_OPENROUTER_KEY_FILE=/root/snaphost-openrouter.key` to the
installation command. Providing only the key-file argument is rejected to
avoid silently enabling AI. An interactive install with AI explicitly enabled
may enter the key at the hidden prompt instead.

For an existing installation, set `LLM_ENABLED=true` and `OPENROUTER_API_KEY`
in the protected `env/production.env` and recreate the application container.
Set `LLM_ENABLED=false` to disable provider calls and the AI cache. A legacy
API key without `LLM_ENABLED=true` no longer enables AI after upgrading.

The application recovery port defaults to `127.0.0.1:8080`. Caddy owns public
80/443. Do not publish the recovery port externally in production; keep the
same restriction in the host firewall.

The application edge proxy on 8081 and TLS authorization gate on 8082 are
container-side contracts only. Compose Caddy reaches `snaphost:8081` and
`snaphost:8082`; neither port is published on the host. Its persistent state is
under `/opt/snaphost/state/caddy`, and deploy/upgrade/rollback recreate Caddy
after the selected backend is healthy. The full behavior and remaining
limitations are in [custom domains](custom-domains.md).

For a short-lived plain-HTTP evaluation only, both the URL and the explicit
opt-in are required:

~~~bash
sudo env \
  SNAPHOST_INSTALL_CONTROL_DOMAIN=panel.example.org \
  SNAPHOST_INSTALL_ACME_EMAIL=acme@example.org \
  SNAPHOST_INSTALL_PUBLIC_URL=http://192.0.2.10:8080 \
  SNAPHOST_INSTALL_ALLOW_HTTP=true \
  /opt/snaphost/infra/snaphostctl install "$VERSION"
~~~

This disables `Secure` on the session cookie. Do not expose credentials or
real projects through that mode.

## Upgrade

Review release notes and migration compatibility first. The no-argument form
fetches `origin/main` and tags, selects the highest strict SemVer tag, prints
it, and pins it. An explicit version is useful for controlled rollout:

~~~bash
sudo snaphostctl upgrade
sudo snaphostctl upgrade v1.3.0
~~~

An upgrade is refused when the checkout is dirty, the tag is absent from
`origin/main`, the version moves backward, or checkout/env/state disagree.
After checking out the target tag, the command installs its AppArmor profile
and invokes that tag's `infra/deploy.sh`. A failed deploy restores the original
checkout and profile. A successful deploy refreshes the systemd units and the
stable `/usr/local/sbin/snaphostctl` copy from the new release.

Upgrade and rollback restart BuildKit, the control plane and Caddy. Run them
when no application build is active; an in-flight build cannot survive the
daemon restart. Existing pre-edge installs must provide
`SNAPHOST_INSTALL_CONTROL_DOMAIN` and `SNAPHOST_INSTALL_ACME_EMAIL` on their
first upgrade so the command can add the new required settings. Obsolete
`DOMAIN_SUFFIX` and Cloudflare-token lines can be removed from an older
production env; current code ignores them.

Do not edit or pull the checkout manually between releases. Operator
customization belongs in the ignored `env/` directory or in a maintained fork
with its own release tags and image prefix.

## Rollback

Preview the runtime decision and verify that the saved image is still local:

~~~bash
sudo snaphostctl rollback --dry-run
~~~

Then roll back the application and its checkout together:

~~~bash
sudo snaphostctl rollback
~~~

The command targets only `state/previous.env`; it never accepts an arbitrary
version. It validates the old Git commit and host artifacts before changing
the runtime, then moves the env file, state, checkout, AppArmor profile and
systemd units to the same release. For the one supported transition from the
legacy SHA model, the stable CLI remains installed even if that old checkout
does not contain it.

After a migration, rollback remains blocked unless the operator has reviewed
the exact schema change and explicitly confirms backward compatibility:

~~~bash
sudo env MIGRATIONS_BACKWARD_COMPATIBLE=true snaphostctl rollback
~~~

That flag does not restore the database. See [rollback](rollback.md) and
[backup and restore](backups.md) before using it.

## Verification and host files

~~~bash
sudo docker compose \
  --project-name snaphost \
  --env-file /opt/snaphost/env/production.env \
  -f /opt/snaphost/infra/docker-compose.prod.yml ps
curl --fail http://127.0.0.1:8080/health
curl --fail https://panel.example.org/health
sudo systemctl status snaphost-backup.timer
sudo cat /opt/snaphost/state/current.env
sudo git -C /opt/snaphost describe --tags --exact-match HEAD
sudo docker compose \
  --project-name snaphost \
  --env-file /opt/snaphost/env/production.env \
  -f /opt/snaphost/infra/docker-compose.prod.yml \
  exec -T buildkitd buildctl --addr tcp://127.0.0.1:1234 du
~~~

The state-file and Git commands must name the same release as
`SNAPHOST_VERSION` in `env/production.env`. Also verify panel login through
HTTPS, an anonymous 401 from a protected API, a real project build, the local
backup timer, and the external monitor.

Important paths:

| Path | Purpose |
| --- | --- |
| `/opt/snaphost` | versioned Git checkout |
| `/opt/snaphost/env/production.env` | protected application/release config |
| `/opt/snaphost/env/backup.env` | optional protected backup credentials |
| `/opt/snaphost/state` | deploy lock and current/previous/progress state |
| `/opt/snaphost/state/caddy` | protected persistent ACME account, certificate and Caddy runtime state |
| `/opt/snaphost/backups` | local SQLite dumps and checksums |
| `/usr/local/sbin/snaphostctl` | stable operator command |
| `/etc/apparmor.d/snaphost-buildkit-rootless` | installed BuildKit profile |
| `/etc/systemd/system/snaphost-backup.*` | installed scheduled-backup units |
| `/etc/systemd/system/snaphost-tls-backup.*` | installed TLS-state backup units; timer disabled until encryption is configured |

## Current proof boundary

`infra/tests/snaphostctl_test.sh` exercises install, resume, protected secrets,
domain isolation, Caddy-state migration, HTTP opt-in, exact-tag upgrades,
newest-version selection, downgrade and host CPU sizing, off-main refusal,
failure compensation, dry-run and checkout-aware rollback.
Those tests fake Git, Docker, AppArmor and systemd. The
[local Caddy integration](../tasks/completed/0004-production-edge.md) additionally
checks real TLS handshakes, on-demand refusal, alias moves and persistent Caddy
state reuse against the pinned image. The
[2026-10-05 public Caddy rehearsal](rehearsals/2026-10-05-public-caddy-control.md)
proves trusted production HTTPS for the panel and verified project domain,
including authenticated panel login. The
[2026-09-04 VPS rehearsal](rehearsals/2026-09-04-vps.md) adds real-host install,
login, local restore, upgrade/rollback and 4 GB build-pressure evidence, and
records the defects it exposed. It used an isolated registry/origin because no
public SemVer release existed, used a plaintext local backup, and did not run
on 1 GB; those remaining acceptance boundaries are not waived by the drill.
[2026-10-05 Node rehearsal](rehearsals/2026-10-05-task7-vds-node.md) records
successful Vite and pinned React/Vite 8 builds on the amd64 VDS, alongside
the older workload's OOM evidence. The
[1 GiB arm64 drill](rehearsals/2026-10-05-task7-1g.md) adds a fresh install,
React build, encrypted systemd SQLite backup and actual DB replacement with
login/password rotation. Those earlier drills used locally built images.
The subsequent [public-release acceptance](rehearsals/2026-10-05-task7-public-release.md)
closed anonymous install, real upgrade/rollback, installed encrypted SQLite
restore and the minimum-host amd64-image build. Task 7 is complete.
The [2026-09-22 edge rehearsal](rehearsals/2026-09-22-production-edge-vps.md)
built the former Caddy image and exercised its TLS path on a 1 CPU, 2 GB VPS
without publishing 80/443. No domains were available, so it does not close the public acceptance
boundary.
