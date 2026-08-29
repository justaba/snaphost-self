# Backup and restore

Status: Current — scheduled backups, retention, encryption, and off-host copy implemented; restore drill proven on staging only
Type: Operations
Updated: 2026-08-04

The durable state requiring backup is PostgreSQL. Redis is transport/cache
state and must not be the only copy of billing or deploy ownership data.

There are two backup mechanisms and they exist for different reasons.

| Mechanism | When | Purpose |
| --- | --- | --- |
| `infra/deploy.sh` | before every migration | the state to return to if this specific release goes wrong |
| `infra/backup.sh run` + systemd timer | daily at 02:20 UTC | the state to return to if the machine or the data is lost |
| `infra/backup.sh tls` + its own timer | daily at 02:40 UTC | the TLS edge's certificate store, so a rebuild does not re-issue every customer's certificate at once |

The TLS store is covered in [custom-domains.md](custom-domains.md) rather than
here: it runs as root instead of the deployment user, encryption is mandatory
rather than optional, and what it protects against is an ACME rate limit rather
than data loss.

Until 2026-08-04 only the first existed. A week without a deploy was a week
without a copy, and nothing ever deleted what it wrote.

## Scheduled backups

[infra/backup.sh](../../infra/backup.sh) does one run end to end:

1. takes `deploy.sh`'s lock, so a dump is never captured mid-migration. If a
   deployment holds it, the run exits successfully without dumping — that
   deployment is taking its own backup;
2. `pg_dump -Fc` out of the running container, using the credentials already
   inside it, so no password passes through this script;
3. **verifies the archive before publishing it**: reads the table of contents
   and requires table data for `wallets`, `transactions`, `deploys`,
   `deploy_sagas`, `api_keys`, `projects`, and `custom_domains`. A dump that
   restores to an empty schema is worse than no dump, because it looks like a
   backup. The `ai_*` tables are cache and accounting logs and are deliberately
   not required;
4. encrypts to `SNAPHOST_BACKUP_AGE_RECIPIENT` with `age`, if set;
5. publishes atomically (`mktemp` + `ln`, never overwriting) with a `.sha256`
   beside it, mode `0600`;
6. copies both to `SNAPHOST_BACKUP_REMOTE` and confirms the object exists;
7. applies retention;
8. records `state/last-backup.env` and pings the heartbeat.

Any failure before step 5 publishes nothing at all.

### Encryption

The host holds only the **public** key, so a compromised host cannot read the
backups it produced yesterday. Generate the keypair somewhere else and keep the
private half offline:

```bash
age-keygen -o snaphost-backup.key      # private — never goes on the VDS
grep 'public key' snaphost-backup.key  # this value goes in backup.env
```

Setting `SNAPHOST_BACKUP_REMOTE` without a recipient is refused: the script
will not upload an unencrypted database to someone else's disk. Server-side
encryption on the bucket is not a substitute — it does not protect against a
leaked access key.

**Losing the private key loses every backup.** Store it the way the
service-account keys are stored, in at least two places, neither of them the
VDS.

### Off-host copy

`SNAPHOST_BACKUP_REMOTE` is an `s3://bucket/prefix` URL against
`SNAPHOST_BACKUP_S3_ENDPOINT` (Yandex Object Storage by default), using the
standard AWS credential resolution. Create the bucket the same way the
Terraform state buckets were created — private, versioned, KMS-encrypted, with
its own service account that can write only this bucket.

A backup that lives only on the machine it was taken from is not a backup: the
staging VDS was lost twice, and both times everything on it went with it.

### Retention

| Group | Default | Rule |
| --- | --- | --- |
| scheduled | 14 days, minimum 7 | `SNAPHOST_BACKUP_KEEP_DAYS` / `_KEEP_MIN` |
| pre-deploy | 90 days, minimum 5 | `SNAPHOST_DEPLOY_BACKUP_KEEP_DAYS` / `_KEEP_MIN` |

Three rules override the age limit, and each exists because of a way automatic
pruning goes wrong:

- the newest N are always kept, however old, so a host that was off for a month
  does not wake up and delete its only copies;
- a dump referenced by `current.env`, `previous.env`, or `in-progress.env` is
  never pruned — those are the two files an operator reaches for mid-incident;
- pre-deploy dumps are pruned far more conservatively, because each one is the
  last state before one specific migration ran.

Checksums are pruned with the dump they belong to and never counted as backups
of their own.

## Installing the timer

`backup.sh` lives at the operator-managed stable path `/opt/snaphost/infra/`,
beside `buildkitd.prod.toml` — not inside `releases/<sha>/`, which is pruned.
Refresh it by hand when the script changes.

On the VDS, as root, once (with the repository checked out at `<repo>`):

```bash
install -o deployer -g deployer -m 0750 <repo>/infra/backup.sh /opt/snaphost/infra/backup.sh
install -o root -g root -m 0644 \
  <repo>/infra/systemd/snaphost-backup.service \
  <repo>/infra/systemd/snaphost-backup.timer \
  /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now snaphost-backup.timer
systemctl list-timers snaphost-backup.timer
```

Configuration goes in `/opt/snaphost/env/backup.env`, mode `0600`, owned by the
deployment user:

```bash
SNAPHOST_BACKUP_AGE_RECIPIENT=age1...
SNAPHOST_BACKUP_REMOTE=s3://snaphost-backups-production/postgres
SNAPHOST_BACKUP_S3_ENDPOINT=https://storage.yandexcloud.net
SNAPHOST_BACKUP_HEARTBEAT_URL=https://hc-ping.com/...
AWS_ACCESS_KEY_ID=...
AWS_SECRET_ACCESS_KEY=...
```

Prerequisites on the host: `age` and `awscli` (`apt install age awscli`). The
unit's `User=` must match the deployment account (`1000:1000`).

Verify without waiting a day:

```bash
sudo -u deployer /opt/snaphost/infra/backup.sh --dry-run run
sudo systemctl start snaphost-backup.service
journalctl -u snaphost-backup.service -n 50
/opt/snaphost/infra/backup.sh list
```

The dry run touches nothing — it does not even take the lock.

## Verify a backup

```bash
/opt/snaphost/infra/backup.sh verify /opt/snaphost/backups/scheduled-<UTC>.dump
```

This checks the checksum and, for an unencrypted dump, that every required
table is present. For an `.age` file only the checksum is checked — decrypt it
with the offline key first to check structure.

For a `deploy.sh` dump the checksum check is the same shape:

```bash
cd /opt/snaphost/backups
sha256sum --check postgres-<UTC>-<sha>-<random>.dump.sha256
```

## Restore

Restore is deliberately manual and must target an isolated PostgreSQL instance
first. `deploy.sh` and `backup.sh` never restore.

```bash
age -d -i snaphost-backup.key -o restore.dump scheduled-<UTC>.dump.age   # if encrypted

docker run -d --name restore-test --network none \
  -e POSTGRES_PASSWORD=throwaway postgres:16

pg_restore --clean --if-exists --no-owner \
  --dbname='<isolated-database-url>' restore.dump
```

Then check the tables that carry money and ownership actually have rows:

```sql
select 'wallets', count(*) from wallets
union all select 'transactions', count(*) from transactions
union all select 'deploys', count(*) from deploys
union all select 'projects', count(*) from projects
union all select 'custom_domains', count(*) from custom_domains;
```

Do not place a database URL in a shared shell history in production.

## What is proven and what is not

Proven on staging 2026-07-08: `deploy.sh` took a custom-format dump, it was
checksum-verified, and it restored into an isolated `--network none` container
with all tables present, leaving the live database untouched.

Not yet proven:

1. a restore drill against a **production** backup;
2. a restore from an **encrypted, off-host** copy — the path that actually gets
   used if the VDS is lost, and the one where a wrong or missing private key
   turns a recoverable incident into data loss;
3. measured recovery point and recovery time objectives.

Item 2 is the one to run first: it is the only step that proves the private key
is where someone thinks it is.

Related: [monitoring](monitoring.md), [rollback](rollback.md),
[production deployment](production-deployment.md).
