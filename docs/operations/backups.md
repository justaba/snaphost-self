# Backup and restore

Status: Backup path implemented and tested with fakes; a current encrypted
off-host restore drill is not recorded
Type: Operations
Updated: 2026-08-30

## What must be backed up

The durable control-plane state is the SQLite database at
/var/snaphost/data/snaphost.db in the snaphost_data volume. It contains the
operator identity, sessions, API keys, projects, deploys, sagas, domains, AI
records and the operator audit log.

Docker images, running containers and BuildKit cache are not contained in this
backup. Restoring SQLite can recover ownership and history, but it does not
recreate a missing image or container. That gap widened with image
reclamation: a deploy whose image the watchdog has released cannot be restarted
from a restore, only rebuilt from its source. deploys.image_deleted_at is what
tells them apart.

## Never copy the live database file

SQLite runs in WAL mode. A plain copy of snaphost.db while the application is
running may omit committed pages in snaphost.db-wal or capture mismatched
files. Use sqlite3 .dump, which reads a consistent transaction.

Both infra/deploy.sh and infra/backup.sh follow that rule.

## Scheduled backup flow

infra/backup.sh run:

1. takes the same lock as deployment so it cannot overlap migration;
2. runs sqlite3 .dump inside the application container;
3. rejects a truncated dump and requires the durable identity, project, deploy,
   saga, key, domain and operator-audit tables;
4. optionally encrypts with age;
5. publishes the file and SHA-256 checksum atomically;
6. optionally copies both to S3-compatible off-host storage and confirms the
   objects exist;
7. applies retention without deleting backups referenced by release state;
8. records last-backup.env and optionally pings a heartbeat URL.

A deployment also creates a verified pre-migration dump. Neither script ever
restores automatically.

## Encryption and off-host storage

Set SNAPHOST_BACKUP_AGE_RECIPIENT to an age public recipient. Keep the private
key off the host and in at least two controlled locations. When
SNAPHOST_BACKUP_REMOTE is set, the script refuses an unencrypted upload.

SNAPHOST_BACKUP_REMOTE uses an s3://bucket/prefix destination and
SNAPHOST_BACKUP_S3_ENDPOINT selects the S3-compatible endpoint. Standard AWS
credential resolution supplies the access key.

A backup that exists only on the machine it protects is not sufficient.

## Verify

~~~bash
/opt/snaphost/infra/backup.sh list
/opt/snaphost/infra/backup.sh verify /opt/snaphost/backups/<backup>.sql
~~~

For an age-encrypted backup, first verify its checksum, then decrypt on a
trusted recovery machine and restore into a throwaway database:

~~~bash
age -d -i snaphost-backup.key -o restore.sql <backup>.sql.age
sqlite3 /tmp/snaphost-restore-check.db < restore.sql
sqlite3 /tmp/snaphost-restore-check.db "pragma integrity_check;"
~~~

Confirm that users, projects, deploys and custom_domains contain the expected
rows. A syntactically valid empty database is not a useful restore.

## Restore

Restore is a reviewed maintenance action:

1. select and checksum-verify the backup;
2. restore it into a throwaway SQLite file and run integrity_check;
3. confirm identity and ownership rows;
4. stop snaphost;
5. take one final recovery dump if the current database is readable;
6. replace the database from SQL while no process has it open;
7. ensure stale -wal and -shm sidecars are absent;
8. start snaphost and verify login, projects, deploy history and health.

Exact volume-mount mechanics depend on the installation layout. The present SSH
deployment layout is not a portable restore contract and Task 7 may change its
paths.

Do not combine database restore with application rollback automatically. An old
database may discard writes made after the selected backup, while an old binary
may be incompatible with the current schema; those are separate decisions.

## Timer

Systemd units live in infra/systemd. The current deployment expects the stable
script path /opt/snaphost/infra/backup.sh and a protected backup.env. Verify the
installed timer and service logs:

~~~bash
systemctl list-timers snaphost-backup.timer
systemctl status snaphost-backup.service
journalctl -u snaphost-backup.service --since "7 days ago"
~~~

The TLS backup subcommand belongs to the host-managed Caddy design. Because the
single-binary custom-domain edge is not complete, it is not proof that current
custom domains are recoverable.

Related: [monitoring](monitoring.md) and [rollback](rollback.md).
