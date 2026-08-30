# Monitoring

Status: Current capabilities documented; no integrated metrics stack
Type: Operations
Updated: 2026-08-30

## Available signals

| Signal | Source |
| --- | --- |
| Process liveness | GET /health |
| Prometheus metrics | GET /metrics |
| Application and build logs | snaphost container logs |
| Deploy log history | GET /api/v1/deploys/<id>/logs |
| Live deploy logs | /ws/logs/<id> |
| User-container stdout and stderr | forwarded by the Docker runtime while the deploy is alive |
| Container state and restart count | Docker inspect and Docker events |
| Backup completion | optional external heartbeat from backup.sh |

The application exposes metrics but the repository does not install Prometheus,
Grafana or an alert manager. There is no durable metrics history.

## External uptime workflow

.github/workflows/uptime.yml periodically runs
.github/scripts/uptime-check.sh. Its default hostnames still describe the
repository owner's inherited production environment. Override
SNAPHOST_MONITOR_API, SNAPHOST_MONITOR_DASHBOARD and
SNAPHOST_MONITOR_DEPLOY_SUFFIX for another environment.

The probe checks:

- health returns 200;
- an anonymous protected API call returns 401;
- the embedded panel returns 200;
- an impossible generated deploy hostname returns 404;
- control-plane and deploy-suffix certificates exceed the expiry threshold.

The unknown-host check is useful only after the environment has a working
wildcard edge. The current portable production edge is incomplete, so this
probe is not an install acceptance test.

GitHub schedules are best-effort and may be delayed or disabled after repository
inactivity. Use an independent monitor for a real installation.

## Host checks

At minimum alert on:

- filesystem usage for Docker images, BuildKit cache, SQLite and backups;
- snaphost and buildkitd restart loops;
- Docker daemon availability;
- SQLite backup age;
- deploy failure rate and sagas stuck in non-terminal states;
- container and host memory pressure during builds;
- TLS expiry once an edge is installed.

Disk monitoring is especially important because deploy expiry currently removes
containers but not their images.

Useful incident commands:

~~~bash
docker compose -f infra/docker-compose.yml ps
docker compose -f infra/docker-compose.yml logs --since 30m snaphost buildkitd
docker stats --no-stream
docker system df
~~~

Do not run broad Docker prune commands as an automatic response: the platform
does not yet distinguish reclaimable images from those needed for rollback or
running deploy records.

## Backup heartbeat

backup.sh sends SNAPHOST_BACKUP_HEARTBEAT_URL only after dump, verification,
optional encryption and upload, and retention all complete. Configure the
receiver with a one-day period and a grace window longer than the scheduled
timer delay.

A failed heartbeat request is logged but does not discard an otherwise valid
backup. Also inspect the systemd timer locally; a heartbeat proves a successful
run, not that a future run remains scheduled.

## Gaps

- metrics are exposed but not scraped;
- no alert exists for image accumulation or stuck sagas;
- the uptime probe does not create a real deploy;
- no 1 GB build-pressure baseline is recorded;
- no portable edge health contract exists before Task 4 and Task 7.

Related: [backup and restore](backups.md) and
[troubleshooting](troubleshooting.md).
