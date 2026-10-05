# Operator account setup

Status: Implemented in v0.1.2
Type: Operations
Updated: 2026-10-05

Fresh installations start without an operator account. Open the private setup
link printed by `snaphostctl install`, choose a login (a name or email), enter
your password twice, and submit. Passwords need at least 12 characters. The
panel signs you in immediately. Login identifiers are case-insensitive.

The link contains a random one-time token in its URL fragment. The fragment
does not reach Caddy or application access logs. The panel removes it from the
current browser URL and sends it only in the setup POST body. Keep the link
private until the account has been created.

If the installer output was closed before opening the link, run on the host:

```bash
sudo snaphostctl setup-link
```

The application keeps the pending token at
`/var/snaphost/data/operator-setup/token`, mode 0600, in a mode-0700 directory
inside its persistent data volume. It survives a restart before setup. The
file is deleted after successful account creation. No login password is
generated, logged, or stored in a host password file.

Unauthenticated setup status returns only `required: true/false`. Setup without
the correct token is refused with 403. A single conditional SQLite write
allows one winning account creation, including concurrent submissions. All
later submissions return 409, and a restart does not reopen setup. Only an
Argon2id password hash is stored; the returned session cookie is HttpOnly and
SameSite=Lax, and Secure in production.

Existing installed accounts retain their login/password during upgrade.
`OPERATOR_EMAIL` no longer creates or renames an account. The installer keeps
its legacy use as an optional default ACME contact; it is not passed to the
application. To change a password after setup, use the panel settings while
signed in. Setup is not a password recovery or public registration endpoint.

## Local development

After `make dev`, obtain a private local URL:

```bash
token=$(docker compose --env-file infra/.env -f infra/docker-compose.yml \
  exec -T snaphost cat /var/snaphost/data/operator-setup/token)
printf 'http://localhost:8080/login#setup-token=%s\n' "$token"
unset token
```

An existing development database already has an operator; use that account.
Do not delete the volume merely to recover a lost password.

## Reinstalling with empty state

This is a separate destructive maintenance action, not an upgrade. Before
replacing state, take a verified SQLite dump, encrypted TLS/config backups and
copies of the project images needed for recovery. Preserve the age identity
separately. Stop the backup timer, application and project containers before
replacing the database volume. A new installation produces a new setup link;
old sessions and passwords do not authenticate against the empty database.

Preserve the existing panel's Caddy certificate state when reusing its hostname.
A fresh database has no projects or domain bindings; after creating your
account, deploy projects and verify their domains again. DNS A records alone
do not restore a deleted ownership binding.

Related: [install/upgrade](install-and-upgrade.md), [backup/restore](backups.md)
and [custom domains](custom-domains.md).

Verified on the VDS in the [fresh-install rehearsal](rehearsals/2026-10-06-operator-setup-reinstall.md):
empty database, private setup required, trusted HTTPS with the restored panel
certificate, and verified recovery copies on the operator workstation.
