# Monitoring

Status: Current capabilities documented; no integrated metrics stack
Type: Operations
Updated: 2026-09-04

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

Deploy expiry now removes the image as well as the container, so the disk
signal that matters has shifted. Watch the BuildKit cache volume, which nothing
reclaims, and watch for images that stay queued: a deploy with an image_ref, a
terminal status and a null image_deleted_at is cleanup the watchdog owes and
has not managed. A count that only grows means the daemon is refusing removals.

~~~bash
sqlite3 /var/snaphost/data/snaphost.db \
  "select count(*) from deploys
   where status in ('failed','deleted')
     and image_ref is not null and image_deleted_at is null;"
~~~

Stopped deploys keep their images on purpose, so they are not a backlog until
STOPPED_IMAGE_GRACE_HOURS has passed. Their footprint is worth watching
separately, because it is bounded by that setting rather than by anything the
platform does:

~~~bash
sqlite3 /var/snaphost/data/snaphost.db \
  "select count(*) from deploys
   where status = 'stopped'
     and image_ref is not null and image_deleted_at is null;"
~~~

Useful incident commands:

~~~bash
docker compose -f infra/docker-compose.yml ps
docker compose -f infra/docker-compose.yml logs --since 30m snaphost buildkitd
docker stats --no-stream
docker system df
~~~

Do not run broad Docker prune commands as an automatic response. The platform
reclaims the images of terminal deploys itself; a blanket prune also removes
the images of running and alias-published deploys, which is a site taken down
rather than disk recovered. docker builder prune is the safe one, and it is
manual because nothing tracks what the cache is worth.

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
- no alert exists for a growing image-cleanup backlog, BuildKit cache growth or
  stuck sagas;
- the audit log is readable in the panel but is not exported anywhere, so
  operator actions are not an alertable signal;
- the uptime probe does not create a real deploy;
- no 1 GB build-pressure baseline is recorded;
- no portable edge health contract exists before Task 4; Task 7's installer
  treats that edge as an explicit prerequisite.

Related: [backup and restore](backups.md) and
[troubleshooting](troubleshooting.md).
