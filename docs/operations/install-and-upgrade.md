# Install and upgrade

Status: Operator commands implemented and covered by fake-command tests; the
required real-VPS install, upgrade, rollback, restore and low-memory rehearsals
are not yet recorded
Type: Operations
Updated: 2026-09-04

This is the supported release layout for one self-hosted machine. The operator
owns one Git checkout at `/opt/snaphost`; its checked-out tag, the application
image, `env/production.env` and `state/current.env` must all name the same exact
release. `snaphostctl` enforces that invariant before it changes the host.

The installer brings up the control plane and BuildKit. It does not install or
configure the production routing edge. Until the Caddy work is complete, the
operator must supply HTTPS for the panel and routing for generated/custom
domains. A temporary plain-HTTP panel is possible only through an explicit,
insecure opt-in described below.

## Host prerequisites

- a systemd Linux host with AppArmor and `apparmor_parser`; the supplied
  rootless-BuildKit profile targets Ubuntu 24.04;
- Docker Engine 24 or newer, Docker Compose v2, and a running Docker daemon;
- Git, GNU coreutils, `awk`, `sed`, `grep`, `flock` and `curl`;
- root access for the install, AppArmor profile and systemd timer;
- outbound HTTPS to GitHub and the public GHCR package;
- DNS and an HTTPS reverse proxy supplied by the operator.

There is not yet a supported minimum RAM claim. In particular, 1 GB has not
been proven while a representative Node project is building; do not size a
production host from idle usage alone.

The GHCR package must be public. If an install asks for `docker login`, package
visibility is wrong; a registry credential is not part of this contract.

## First install

Choose a published strict SemVer tag. Do not install `main`, `latest`, or a Git
SHA.

~~~bash
VERSION=v1.2.3
sudo git clone https://github.com/justaba/snaphost-self.git /opt/snaphost
sudo git -C /opt/snaphost checkout --detach "$VERSION"
sudo install -m 600 /dev/null /root/snaphost-openrouter.key
sudoedit /root/snaphost-openrouter.key
~~~

Put only the OpenRouter key in that file, with no shell assignment. Keeping it
in a protected file avoids putting it in shell history or the process list.

For a non-interactive install behind an HTTPS edge:

~~~bash
sudo env \
  SNAPHOST_INSTALL_DOMAIN_SUFFIX=apps.example.net \
  SNAPHOST_INSTALL_OPERATOR_EMAIL=operator@example.net \
  SNAPHOST_INSTALL_OPENROUTER_KEY_FILE=/root/snaphost-openrouter.key \
  SNAPHOST_INSTALL_PUBLIC_URL=https://panel.example.net \
  /opt/snaphost/infra/snaphostctl install "$VERSION"
~~~

Omit `SNAPHOST_INSTALL_OPERATOR_EMAIL` to use `operator@localhost`. Omit
`SNAPHOST_INSTALL_PUBLIC_URL` when HTTPS is not ready; the deploy then performs
only its internal readiness check. An interactive terminal may omit the domain
and key-file variables and answer the prompts instead.

The command:

1. refuses a dirty checkout or a checkout not at the requested tag;
2. writes `/opt/snaphost/env/production.env` with mode `0600`;
3. derives the numeric group of `/var/run/docker.sock`;
4. installs and reloads the BuildKit AppArmor profile;
5. pulls the exact version, migrates SQLite and waits for Docker health;
6. installs and starts `snaphost-backup.timer`;
7. installs the stable command as `/usr/local/sbin/snaphostctl`;
8. prints the generated operator password once.

Store that password immediately, sign in, and change it in the panel. The
change revokes the bootstrap password and other sessions. The original value
can remain in Docker's retained container log, so treat the password change as
part of installation.

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

The default published address is `0.0.0.0:8080`. Restrict it with
`SNAPHOST_BIND_ADDRESS=127.0.0.1` when the reverse proxy is on the same host,
and enforce the same restriction in the host firewall.

For a short-lived plain-HTTP evaluation only, both the URL and the explicit
opt-in are required:

~~~bash
sudo env \
  SNAPHOST_INSTALL_DOMAIN_SUFFIX=apps.example.net \
  SNAPHOST_INSTALL_OPENROUTER_KEY_FILE=/root/snaphost-openrouter.key \
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
sudo systemctl status snaphost-backup.timer
sudo cat /opt/snaphost/state/current.env
sudo git -C /opt/snaphost describe --tags --exact-match HEAD
~~~

The last two commands must name the same release as `SNAPHOST_VERSION` in
`env/production.env`. Also verify panel login through HTTPS, an anonymous 401
from a protected API, a real project build, off-host backup delivery, and the
external monitor.

Important paths:

| Path | Purpose |
| --- | --- |
| `/opt/snaphost` | versioned Git checkout |
| `/opt/snaphost/env/production.env` | protected application/release config |
| `/opt/snaphost/env/backup.env` | optional protected backup credentials |
| `/opt/snaphost/state` | deploy lock and current/previous/progress state |
| `/opt/snaphost/backups` | local SQLite dumps and checksums |
| `/usr/local/sbin/snaphostctl` | stable operator command |
| `/etc/apparmor.d/snaphost-buildkit-rootless` | installed BuildKit profile |
| `/etc/systemd/system/snaphost-backup.*` | installed scheduled-backup units |

## Current proof boundary

`infra/tests/snaphostctl_test.sh` exercises install, resume, protected secrets,
HTTP opt-in, exact-tag upgrades, newest-version selection, downgrade and
off-main refusal, failure compensation, dry-run and checkout-aware rollback.
Those tests fake Git, Docker, AppArmor and systemd. They do not replace Task 7's
acceptance drills on a fresh real VPS.
