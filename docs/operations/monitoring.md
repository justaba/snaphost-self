# Monitoring

Status: Current — external probe and backup heartbeat implemented; in-host metrics collection not
Type: Operations
Updated: 2026-08-04

Production had no monitoring at all until 2026-08-04: every service exposed
`/metrics`, nothing scraped it, and an outage was discovered by a user. What
exists now is deliberately the cheapest thing that removes that failure mode,
not a monitoring stack.

## What is watched

| Signal | By what | Detects |
| --- | --- | --- |
| Public API, dashboard, runtime router, TLS expiry | [uptime.yml](../../.github/workflows/uptime.yml) every 10 minutes | production is down or about to expire |
| A backup happened | `backup.sh` heartbeat → external dead-man's switch | the backup timer stopped and nobody noticed |

## The external probe

[.github/scripts/uptime-check.sh](../../.github/scripts/uptime-check.sh) runs
six checks. Each one is there because it covers a layer the others do not:

1. `GET api.snaphost.ru/health` → `200`. The gateway container answers.
2. `GET api.snaphost.ru/api/v1/deploys` → `401`. JWT middleware is running.
   `/health` is registered *before* all middleware, so it stays `200` even if
   authentication is broken — this check is what notices that.
3. `GET snaphost.ru/` → `200`. Caddy and the `frontend/current` symlink.
4. `GET proj-monitor-probe-does-not-exist.snaphost.pw/` → `404`. The entire
   runtime path: wildcard DNS, the Yandex API Gateway, `router-svc`, and its
   route lookup back into `user-billing`. A `502`/`504` means the router is
   down; a `200` would mean it resolved a host that does not exist.
5, 6. Certificates for the control plane and the deploy suffix have more than
   14 days left. Renewal is automatic, so its failure is silent — and Let's
   Encrypt renews at 30 days, so 14 means two renewal cycles already failed.

Run the identical checks by hand during an incident:

```bash
bash .github/scripts/uptime-check.sh
```

Override targets with `SNAPHOST_MONITOR_API`, `SNAPHOST_MONITOR_DASHBOARD`,
`SNAPHOST_MONITOR_DEPLOY_SUFFIX`, and `SNAPHOST_MONITOR_CERT_MIN_DAYS`.

On failure the workflow opens one issue labelled `uptime`, comments on it every
subsequent failing run rather than opening more, and closes it when the checks
pass again. Alerting is therefore GitHub's issue notification — configure
`Watch → Issues` on the repository for anyone who should be paged.

### What this does not do

Say it plainly, because the gap matters more than the coverage:

- **GitHub schedules are best-effort.** Runs routinely start 10–30 minutes
  late, and a scheduled workflow is disabled automatically after 60 days of
  repository inactivity. This detects "production has been down for a while",
  not a 90-second blip, and it can silently stop running.
- **It sees nothing inside the VDS.** Disk filling up, a container in a restart
  loop behind a healthy gateway, Redis rejecting writes, saga workers wedged —
  all invisible until they reach the public surface.
- **No metrics are collected.** Prometheus endpoints exist on every service and
  nothing reads them, so there is no history to look at after an incident.
- **One deploy is not tested.** The probe never creates a real deploy, so a
  broken build pipeline registers as healthy.

A real second step is scraping the existing `/metrics` endpoints and alerting
on saga failure rate, wallet reserve/commit mismatch, and container restart
counts. That is not built.

## The backup heartbeat

A dead-man's switch needs an observer outside the host: if the machine is off,
it cannot report that it is off. `backup.sh` pings
`SNAPHOST_BACKUP_HEARTBEAT_URL` only after a backup has been dumped, verified,
encrypted, uploaded, and pruned — a partial success does not ping.

Any dead-man's-switch receiver works. With
[healthchecks.io](https://healthchecks.io) (free tier is enough for one check):

1. create a check with period 1 day and grace 6 hours, matching the timer;
2. put its ping URL in `/opt/snaphost/env/backup.env` as
   `SNAPHOST_BACKUP_HEARTBEAT_URL`;
3. set the alert channel (email or Telegram).

A failed ping is logged as a warning and never fails the backup — the backup
already succeeded, and losing the notification is not a reason to discard it.

Belt and braces on the host itself, since the heartbeat only covers the
"nothing ran" case:

```bash
systemctl list-timers snaphost-backup.timer
systemctl status snaphost-backup.service
journalctl -u snaphost-backup.service --since '7 days ago'
```

## User container runtime logs

**Currently disabled in production.** Attaching the dedicated log group is
refused by Yandex with `PermissionDenied: Not enough permissions to use log
group`, and folder-level `logging.writer`/`logging.reader`/`logging.viewer` on
the runner service account do not satisfy it. Production runs with
`YANDEX_RUNTIME_LOGS_DISABLED=true`, so user containers write to the folder's
default log group — queryable, but mixed with everything else in the folder.
See [Task 13a](../inherited/active/0013-observability-and-user-feedback.md) for what
was tried and what to try next.

When it is working, every Serverless Container revision carries `log_options`
and a deployed application's `stdout`/`stderr` lands in the group named by
`YANDEX_LOG_GROUP_ID` (Terraform output `runtime_log_group_id`).

Collection is best-effort by design: an unconfigured group sends no options at
all, and a revision rejected over its log group is redeployed once without
them, with a warning in the deploy log. A deploy never fails because logs
cannot be collected.

This stream is **operator-only**. It is deliberately not forwarded to the deploy
owner's WebSocket — `StreamLogs` on the Yandex backend refuses rather than
publishing, because runtime output is noisy and can carry infrastructure
detail. Users get build logs plus a failure recommendation instead (Task 13b).

Reading a specific deploy's output:

```bash
# The revision's container is named for the deploy, so filter on it.
yc logging read \
  --group-id "$(terraform -chdir=terraform/yandex output -raw runtime_log_group_id)" \
  --filter 'json_payload.container_id = "<container-id>"' \
  --since 1h

# Or follow everything while reproducing a report.
yc logging read --group-id <id> --follow
```

Retention is `runtime_log_retention` (default 168h). It is short on purpose:
this is the noisiest data the platform holds and it stops being useful once the
deploy is gone.

`YANDEX_RUNTIME_LOGS_DISABLED=true` turns collection off. It exists for a
deliberate choice, not as a default — with it on, a crashed user container
leaves no evidence anywhere, which is the state Task 13a was written to fix.

## Checking that the probe itself works

The probe failing open — passing while production is down — is the failure mode
that makes monitoring worse than none. Force a failure occasionally:

```bash
SNAPHOST_MONITOR_API=https://api.snaphost.invalid bash .github/scripts/uptime-check.sh
```

It must exit non-zero and name the failing checks.

Related: [backups](backups.md), [rollback](rollback.md),
[troubleshooting](troubleshooting.md).
