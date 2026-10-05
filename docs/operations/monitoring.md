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

## External uptime monitoring

This repository deliberately does not schedule probes for installed hosts. The
previous workflow watched the repository owner's inherited domains and opened
incidents in this repository, which is not a suitable ownership model for a
self-hosted installation. Configure an independent monitor under the operator's
account instead.

At minimum, probe:

- health returns 200;
- an anonymous protected API call returns 401;
- the embedded panel returns 200;
- an unknown project-domain SNI cannot obtain a certificate;
- control-plane and attached project-domain certificates exceed the expiry
  threshold.

Run the monitor outside the target host and route alerts through an operator-owned
channel. The project-domain and TLS probes become meaningful only after public
DNS has been configured for that installation.

## Host checks

At minimum alert on:

- filesystem usage for Docker images, BuildKit cache, SQLite and backups;
- snaphost and buildkitd restart loops;
- Docker daemon availability;
- SQLite backup age;
- deploy failure rate and sagas stuck in non-terminal states;
- container and host memory pressure during builds;
- TLS expiry once an edge is installed.

Deploy expiry now removes the image as well as the container, while BuildKit
periodically manages its own cache against 512 MB reserved, 4 GB maximum-used
and 5 GB free-space targets. Watch both mechanisms: a deploy with an image_ref,
a terminal status and a null image_deleted_at is cleanup the watchdog owes and
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
docker compose -f infra/docker-compose.yml exec -T buildkitd \
  buildctl --addr tcp://127.0.0.1:1234 du
docker stats --no-stream
docker system df
~~~

Do not run broad Docker prune commands as an automatic response. The platform
reclaims the images of terminal deploys itself; a blanket prune also removes
the images of running and alias-published deploys, which is a site taken down
rather than disk recovered. BuildKit GC is automatic and isolated to its own
cache. In an emergency, run `buildctl prune --all` inside the `buildkitd`
service, understanding that the next builds will be cold.

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
- no packaged black-box monitor or synthetic deploy check is supplied;
- no 1 GB build-pressure baseline is recorded;
- the edge has route and TLS-authorization probes, but no packaged public
  DNS/ACME synthetic check.

Related: [backup and restore](backups.md) and
[troubleshooting](troubleshooting.md).
