# Backup and restore

Status: Database backup implemented; external storage optional
Type: Operations
Updated: 2026-10-05

## Durable state

The SQLite database in the `snaphost_data` volume contains operator identity,
sessions, API keys, projects, deploys, sagas, project domains, AI records and
the audit log. Docker images, containers and BuildKit cache are outside it.

Caddy stores its ACME account, certificates and private keys under
`/opt/snaphost/state/caddy`. Compose mounts this directory across container
recreation. The [public rehearsal](rehearsals/2026-10-05-public-caddy-control.md)
verified fingerprint reuse both after Caddy recreation and after restoring
`backup.sh tls` into an isolated Caddy container. The operator has chosen not
to require external storage. Loss of the VPS or that directory will cause
Caddy to create new ACME state and issue replacement certificates.

## Optional local TLS backup and isolated restore

`backup.sh tls` requires an age recipient even when no remote is configured.
It streams the Caddy `data` directory through `tar` directly into age and
writes only a `0600` encrypted archive and SHA-256 sidecar under the protected
backup directory. The private age identity must be protected separately from
the public recipient. Keeping both identity and archive only on the same VPS
tests restore mechanics but does not provide disaster recovery.

```bash
sudo age-keygen -o /root/snaphost-tls-restore.key
recipient=$(sudo age-keygen -y /root/snaphost-tls-restore.key)
sudo env \
  SNAPHOST_BACKUP_AGE_RECIPIENT="$recipient" \
  SNAPHOST_TLS_STATE_DIR=/opt/snaphost/state/caddy/data \
  SNAPHOST_BACKUP_DIR=/opt/snaphost/backups \
  /opt/snaphost/infra/backup.sh tls
sudo /opt/snaphost/infra/backup.sh verify \
  /opt/snaphost/backups/tls-YYYYMMDDTHHMMSSZ.tar.gz.age
```

For a restore drill, check the sidecar with `sha256sum -c`, then stream
`age -d -i /root/snaphost-tls-restore.key <archive> | tar -xzf -` into a
protected empty directory. Mount its `data` as `/data` in an isolated Caddy
container using the same pinned image and Caddyfile. Bind test ports only to
loopback, send the original SNI with `openssl s_client`, and compare the
certificate fingerprint with the live service. Confirm HTTPS 200 for a
previously issued domain and zero `obtaining new certificate` events. Remove
the decrypted test directory afterward. Restored directories should be
`0700` and private-key files `0600`.

## Database backup

Never copy the live SQLite file. It runs in WAL mode, so a plain file copy can
omit committed pages or capture mismatched database and WAL files.
`infra/backup.sh run` uses `sqlite3 .dump` inside the application container:

1. it takes the deployment lock;
2. creates a consistent SQL dump;
3. rejects truncation and missing durable tables;
4. optionally encrypts with age;
5. publishes the file and SHA-256 checksum atomically;
6. optionally uploads both to an S3-compatible destination;
7. applies retention and records `last-backup.env`.

The S3 and age settings remain supported options in `backup.sh`; neither is
required by installation or Task 4.

## Verify

```bash
/opt/snaphost/infra/backup.sh list
/opt/snaphost/infra/backup.sh verify /opt/snaphost/backups/<backup>.sql
```

For an encrypted backup, verify its checksum, decrypt it on a trusted machine
and test it in a throwaway database:

```bash
age -d -i snaphost-backup.key <backup>.sql.age \
  | sqlite3 /tmp/snaphost-restore-check.db
sqlite3 /tmp/snaphost-restore-check.db 'pragma integrity_check;'
rm -f /tmp/snaphost-restore-check.db
```

Confirm that `users`, `projects`, `deploys` and `custom_domains` contain the
expected rows.

## Restore

Database restoration is a reviewed maintenance action:

1. checksum-verify the selected dump;
2. restore it into a throwaway SQLite file and run `integrity_check`;
3. confirm identity and ownership rows;
4. take a final recovery dump while the current application is still running
   and its database remains readable;
5. stop SnapHost;
6. replace the database from SQL while no process has it open;
7. remove stale `-wal` and `-shm` sidecars;
8. start Snaphost and verify login, projects, deploy history and health.

Do not combine database restore with application rollback automatically. The
selected dump may lose later writes, and an older binary may not support the
current schema.

The [isolated 1 GiB drill](rehearsals/2026-10-05-task7-1g.md#encrypted-scheduled-backup-and-actual-database-replacement)
records an encrypted dump from the installed systemd service, checksum and
integrity verification, actual volume replacement and successful login/password
rotation. It includes executable restore commands and checks mode 0600 and
UID/GID 1000:1000 on the live DB. It used local storage and a local release tag.

## Timer

`snaphostctl install` enables `snaphost-backup.timer` and leaves the optional
private-key-bearing TLS timer disabled. Check the active database schedule with:

```bash
systemctl list-timers snaphost-backup.timer
systemctl status snaphost-backup.service
journalctl -u snaphost-backup.service --since '7 days ago'
```

Related: [project domains](custom-domains.md), [monitoring](monitoring.md) and
[rollback](rollback.md).
