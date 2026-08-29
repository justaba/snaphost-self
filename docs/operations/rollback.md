# Rollback

Status: Implemented and covered by tests; the 2026-08-03 production rehearsal predates the one-service collapse and SQLite, and the path it exercised was broken from item 5 until 2026-08-29
Type: Operations
Updated: 2026-08-29

Application images are identified by immutable Git SHA. A rollback should
restore the previously recorded SHA, not rebuild an old branch or use `latest`.

Required rollout record:

- previous SHA;
- target SHA;
- migration version;
- backup identifier;
- start/end time;
- smoke result.

Rollback is safe only when database migrations are backward compatible with
the previous application. Destructive migrations need an explicit forward-fix
or database restore plan before rollout.

`infra/deploy.sh` records current and previous successful SHAs atomically and
pulls target and previous images before rollout. The rollback command accepts no
image argument; it can only use the saved previous SHA, verifies those images
exist locally, performs ordered updates, readiness, smoke, and swaps state only
after success:

```bash
infra/deploy.sh rollback
```

`infra/deploy.sh --dry-run rollback` performs read-only preflight against the
saved state and local images. It does not create state/backup directories,
`deploy.lock`, or state files.

Before migrations, image rollback is safe because the schema has not changed.
After either migration job begins, compatibility is not assumed. Rollback is
blocked unless an operator has reviewed the exact migrations and explicitly
confirms:

```bash
MIGRATIONS_BACKWARD_COMPATIBLE=true infra/deploy.sh rollback
```

On deployment failure after migration start without that confirmation, state
becomes `manual-intervention-required`. The script reports the saved previous
SHA and backup path but does not start old images or restore the database. Inspect
service/migration logs and the migration ledger, verify the backup checksum,
then choose a forward fix or a separately reviewed restore. Database restore is
never automatic.

An interrupted rollback also leaves `in-progress.env` when one exists. Follow
the stale-state diagnosis and explicit removal procedure in the
the upstream production runbook (removed with the fork); there is no force or
automatic continue path.

## What the tests cover

[infra/tests/deploy_test.sh](../../infra/tests/deploy_test.sh) runs the rollback
path against fake commands — no Docker daemon, database, credentials, or
network — and CI runs it on every push. Twelve scenarios are covered:

| Scenario | Expected |
| --- | --- |
| before migrations | proceeds |
| after migrations, no confirmation | refused |
| after migrations, `MIGRATIONS_BACKWARD_COMPATIBLE=true` | proceeds |
| success | records restored SHA and `status=rollback-success` |
| rolling back twice | returns to the original SHA |
| after migrations | `migration_status` is preserved, not reset |
| `previous.env` missing | refused |
| `previous.env` holds a malformed SHA | refused |
| saved rollback image absent locally | refused **before** any container is touched |
| smoke fails | no success is recorded; state still names the old SHA |
| another operation holds the lock | refused |
| any of the above | no secret in output or state |

Tests are not a rehearsal. They prove the script's decisions, not that the
production host has the images, state files, and disk space the script assumes.

## Production rehearsal

Rollback was exercised on staging on 2026-07-08 (blocked without the
compatibility flag, succeeded with it). Staging no longer exists, and the
rehearsal has never run against production — so the first real production
rollback would be its own first test, during an incident. Run it deliberately
instead, at a quiet time, with someone watching.

The rehearsal is: deploy the current SHA again so a known-good `previous.env`
exists, roll back, verify, then roll forward. It is two ordinary operations in
sequence, and every step is one the script already performs during a normal
deployment.

### Invoking the script correctly

`deploy.sh` lives inside the release directory, and **its defaults are wrong for
production**: they point at `/opt/snaphost/infra/docker-compose.prod.yml`, a
leftover from the manual bootstrap that is not the deployed release. Running it
without the environment block would validate and roll out against a stale
manifest.

`/opt/snaphost/current` points at the deployed release and is maintained by
[deploy-remote.sh](../../.github/scripts/deploy-remote.sh). It was missing
between 2026-08-03 and 2026-08-04 — see
[the stdin defect](#why-current-went-missing) — so prefer the explicit release
path when the two could disagree.

Pass exactly what CD passes:

```bash
cd /opt/snaphost      # docker compose stats its working directory
R=/opt/snaphost/releases/<deployed-sha>

export SNAPHOST_COMPOSE_FILE=$R/docker-compose.prod.yml \
       SNAPHOST_COMPOSE_PROJECT=snaphost \
       SNAPHOST_ENV_FILE=/opt/snaphost/env/production.env \
       SNAPHOST_STATE_DIR=/opt/snaphost/state \
       SNAPHOST_BACKUP_DIR=/opt/snaphost/backups \
       SNAPHOST_GHCR_TOKEN_FILE=/opt/snaphost/secrets/ghcr-token \
       SNAPHOST_PUBLIC_SMOKE_URL=https://api.snaphost.ru
```

Run as `deployer` (uid 1000), never as root: state files and backups written by
root cannot be replaced by the next CD run, which connects as `deployer`. If
you reached the host as root, `sudo -u deployer` needs a working directory the
deployer can read — otherwise `docker compose` fails with `stat .: permission
denied`.

### Procedure

**Before starting**

```bash
cat /opt/snaphost/state/current.env      # note sha, migration_status, backup_path
cat /opt/snaphost/state/previous.env     # must exist
ls -la /opt/snaphost/state/in-progress.env   # must NOT exist
df -h /opt/snaphost
/opt/snaphost/infra/backup.sh list       # a recent backup must exist
```

If `previous.env` is missing, rollback is impossible and the rehearsal is
instead "deploy a second SHA first". If `in-progress.env` exists, stop and
follow the interrupted-deployment procedure — do not rehearse on top of it.

**Dry run first.** It reads state and local images and writes nothing:

```bash
"$R/deploy.sh" --dry-run rollback
```

A failure here is the rehearsal's real payoff: it means the saved images were
pruned from the local Docker cache, or the state files disagree, and you found
out without an outage.

**Roll back.** `migration_status` in `current.env` decides the invocation:

```bash
# migration_status=not-started
"$R/deploy.sh" rollback

# migration_status=applied — only after reading the migrations between the two
# SHAs and confirming the older application tolerates the newer schema
MIGRATIONS_BACKWARD_COMPATIBLE=true "$R/deploy.sh" rollback
```

When `current.env` and `previous.env` hold the same SHA there are no migrations
between them, so the confirmation is trivially and verifiably true — which is
what makes that case safe to rehearse.

**Verify from outside**, not from the host:

```bash
bash .github/scripts/uptime-check.sh
```

All six checks must pass. Then confirm the state swapped and stayed owned by
the deployment user:

```bash
cat /opt/snaphost/state/current.env   # status=rollback-success
cat /opt/snaphost/state/previous.env
stat -c '%U:%G %n' /opt/snaphost/state/*.env   # must be deployer:deployer
```

**Roll forward** if the two SHAs differed, to leave production on the intended
release:

```bash
"$R/deploy.sh" deploy <the-SHA-you-started-on>
```

**Record** in the task log: both SHAs, `migration_status`, the backup
identifier, start and end times, and the smoke result. That record is the point
of the exercise — the next operator needs to know it has been done and what it
cost.

### Rehearsal record — 2026-08-03

Run against production (`135.106.166.76`) at 22:49–22:50 UTC. Kept as a record
of what happened, not as a description of the current script: it predates both
the collapse to one service and the move to SQLite, and the rollback path it
exercised was broken shortly afterwards — `rollback_to` went on naming the
seven services that stopped existing, and nothing rehearsed it again. **No
rehearsal has been run since.**

| Field | Value |
| --- | --- |
| SHA before / after | `54c5ebdf` / `54c5ebdf` (identical — see below) |
| `migration_status` | `applied` |
| Backup on disk | `postgres-20260803T112024Z-54c5ebdf….mUOAjm.dump` |
| Duration | 6.5 s |
| Smoke | passed; all six external checks passed afterwards |
| Containers recreated | none — every container ID and uptime unchanged |
| Restart counts | 0 |

Confirmed working:

- the migration guard refused the rollback without
  `MIGRATIONS_BACKWARD_COMPATIBLE`, against real production state;
- dry run passed preflight and wrote nothing (state checksums unchanged, no
  `deploy.lock`, no `in-progress.env`);
- the real rollback validated the rendered Compose, confirmed the saved images
  were present locally, walked all ten services in dependency order with
  readiness and internal HTTP probes, and passed the public smoke — health,
  authenticated route lookup `404`, and wrong-secret `401`;
- state was swapped atomically and both files stayed `deployer:deployer`, so
  the next CD run can still write them;
- the lock was released and no `in-progress.env` remained.

**What this did not prove.** `current.env` and `previous.env` held the same SHA,
because the last two production deployments were the same release. Compose
therefore had no image change to make and left every container running
untouched. The orchestration, the guards, and the state machine are proven; an
actual **image version change** and container recreation are not. Proving that
needs two different SHAs on production, which is a real deployment and a real
migration review, not a rehearsal.

`current.env` now reads `status=rollback-success` rather than `success`. That is
accurate and the next deployment overwrites it.

## Why `current` went missing

The rehearsal found `/opt/snaphost/current` absent even though every CD step
had reported success. The cause is worth knowing, because it can silently
discard any command and still exit `0`.

CD runs the remote half of a deployment as `ssh host bash -s <<'REMOTE'`, so
**the deployment commands and the script text share one stdin**. `docker
compose exec -T` and `docker compose run` forward stdin to the container.
`deploy.sh` uses both — for the backup (`pg_dump` then, `sqlite3 .dump` now),
the migration job, and the internal HTTP probes — so they drained the pipe that
`bash -s` was still
reading its own source from. Everything after the deploy call was discarded
before bash ever parsed it, which in this script was exactly one line:

```bash
mv -Tf -- "$link_tmp" "$root/current"
```

Bash then reached EOF and exited `0`, so the step passed. Reproduced on the
production host: a three-line piped script whose middle command is `cat` or
`docker compose exec -T` never runs its third line, and reports success.

Fixed in three places:

- `deploy.sh` and `backup.sh` redirect `</dev/null` on every `compose exec` and
  `compose run`, so no caller's stdin can be consumed;
- `deploy-remote.sh` redirects `</dev/null` on the `deploy.sh` invocations as a
  second layer, and asserts `[[ -L "$root/current" ]]` afterwards so a
  disappearing symlink fails the run instead of passing it;
- `deploy_test.sh` pipes a script into `bash -s` and asserts the line after the
  deploy still runs. Its fake `docker` drains stdin exactly as the real one
  does, and the test fails if any redirect is removed.

The lesson generalizes: any command in a piped-to-shell script that might read
stdin needs `</dev/null`, and a CD step that reports success is not evidence
that its last line ran.

**If the rollback itself fails**, the runtime is somewhere between the two
releases. Do not retry blindly: read `in-progress.env`, check which services
report which image, and follow the interrupted-deployment procedure in the
the upstream production runbook (removed with the fork). The database backup
taken before the original deployment is the floor, and restoring it is a
separate, explicitly reviewed decision.
