# Troubleshooting

Status: Current
Type: Operations
Updated: 2026-09-01

Start with the failing boundary rather than an inherited service name. The
current stack has one application process, BuildKit, Docker and an edge.

## Application does not start

Check snaphost logs for configuration validation. WEBHOOK_SECRET and
OPENROUTER_API_KEY are required. RUNNER_BACKEND must be docker. With
STRICT_IMAGE_VALIDATION=true, ALLOWED_IMAGE_PREFIXES must be non-empty.

If Docker calls fail with permission denied, compare DOCKER_SOCKET_GID with the
numeric group of /var/run/docker.sock on the host.

If the process rejects its memory configuration, inspect the container cgroup
limit and GOMEMLIMIT_RESERVE_MB. The reserve must leave a usable Go heap.

## Cannot log in

On a fresh volume, read the generated operator password from the first startup
logs. It is printed once and is not recoverable from SQLite.

Confirm OPERATOR_EMAIL matches the bootstrapped row; changing the environment
after bootstrap does not rename the account. Behind TLS, set
SESSION_COOKIE_SECURE=true. Over plain local HTTP, forcing it true prevents the
browser from returning the cookie.

Repeated failures may hit the in-memory per-address limiter. A successful login
clears the counter.

## Build fails before clone

Check ALLOWED_GIT_HOSTS, DNS, TLS hostname validation and whether the resolved
address was rejected as loopback, private or metadata space. For a private
repository, the short-lived credential exists only in memory and is lost on
process restart.

For archives, inspect upload and unpack size limits and path-containment errors.

## Dockerfile generation fails

Built-in templates run before the LLM. Inspect detection logs and the generated
Dockerfile validation result. OpenRouter errors matter only when no template
matches.

Both user Dockerfiles and generated Dockerfiles must use an allowed, tagged base
image and must work with the runtime's read-only root filesystem.

## BuildKit or image load fails

Check buildkitd health and connectivity at BUILDKIT_HOST:

~~~bash
docker compose -f infra/docker-compose.yml logs --since 20m buildkitd snaphost
docker compose -f infra/docker-compose.yml exec buildkitd \
  buildctl --addr tcp://127.0.0.1:1234 debug workers
~~~

The build streams a Docker exporter into the host daemon. There is no registry
to inspect. After a successful build, the image should exist locally under
snaphost/proj-<hash>:<deploy-id>.

Transient build retries are not implemented; retrying the deploy is
currently an operator action.

## Container starts but deploy fails

The runtime reports running only after it can dial the application on the
detected port. The application must listen on the injected PORT value and on an
address reachable outside its own loopback interface, normally 0.0.0.0.

Inspect the deploy logs and container logs before the failed container is
removed. Common causes are a fixed port, writing to the read-only root
filesystem, missing runtime files or immediate process exit.

## Generated URL does not resolve

Confirm Traefik is running, the user container is attached to snaphost-net and
its labels contain the full generated hostname. DOMAIN_SUFFIX also needs DNS
that resolves wildcard hosts to the edge. Local resolver behavior for
*.localhost is platform-dependent.

A 404 means no matching running route; 502 usually means the edge matched but
could not reach the container.

## Custom domain remains pending

Read last_error from GET /api/v1/domains and query the exact returned TXT record.
The common states are txt_not_found, txt_mismatch and dns_lookup_failed.

Verification alone does not make the site reachable. The single-binary
Docker/Caddy routing path is incomplete; see
[custom domains](custom-domains.md).

## Running-state persistence fails

The transition to a live deployment updates deploys and deploy_sagas in one
SQLite transaction. If that transaction fails, runtime returns a transient
error, stops the uncommitted container and restores the deploy to building so
the saga can retry. It does not publish the URL as ready.

Inspect logs around failed to persist running deployment, failed to stop
uncommitted container and failed to restore deploy status. The latter two mean
automatic cleanup did not complete and require checking the container and
deploy row before re-enqueueing the saga.

## TTL cleanup or disk use

Check WATCHDOG_INTERVAL_SEC, the deploy's stored container ID, alias target and
expiry fields. Aliased deploys are intentionally retained.

The watchdog runs three sweeps per tick: expiry stops deploys past their TTL,
reclamation moves deploys stopped for longer than STOPPED_IMAGE_GRACE_HOURS to
deleted, and the image sweep releases the images of failed and deleted rows.

A stopped deploy keeps its image on purpose so it can be started again, so
disk held by stopped deploys is expected until the grace expires. Setting
STOPPED_IMAGE_GRACE_HOURS to 0 disables reclamation entirely and those images
are then kept indefinitely.

One image can exist that no sweep will ever find, and there is exactly one way
to produce it: the build recorded the artifact, could not write it to the
database after two attempts, and could not remove it from the daemon either —
both are unreachable at once. Nothing durable can be written in that state, so
the record is a log line. Search for it:

~~~bash
docker compose -f infra/docker-compose.yml logs snaphost \
  | grep "could not be recorded or removed"
~~~

It carries remove_image_command with the exact docker image rm to run.

A deploy with an image_ref, a status of failed or deleted and a null
image_deleted_at is work the sweep still owes; the row stays queued until
Docker confirms the image is gone, so a backlog that only grows means removals
are being refused. Read the watchdog image cleanup complete log line for the
checked and removed counts, and look for failed to remove deploy image above it.

The two sweeps are independent, so an expiry failure does not explain an image
one, and neither does the reverse. Do not automate docker image prune: a
blanket prune also removes the images of running and alias-published deploys.
docker builder prune is the manual step for the BuildKit cache, which nothing
reclaims.

## Deleting a project is refused

DELETE /api/v1/projects/:id answers with a specific reason and the panel shows
it verbatim:

| Code | Meaning |
| --- | --- |
| project_busy (409) | A deploy or saga of the project is still building or provisioning. The refusal exists because that work runs in-process and could create a container after the cleanup plan was taken. Wait for it, or let it fail. |
| cleanup_failed (502) | A container would not stop or an image would not go. Nothing was deleted. Check the Docker daemon and repeat. |
| runtime_unavailable (503) | The project holds a container or image and no runtime client is configured. |
| project_not_found (404) | Already gone. |

A 409 that never clears usually means a saga stranded in a non-terminal step
rather than a build actually running. Inspect deploy_sagas.current_step for the
project's deploys; the resume sweeper should move it, and a saga sitting at
compensating is the one case that needs looking at.

Every successful deletion writes an admin_audit_log row, shown in the panel
under Настройки and readable through GET /api/v1/audit.

## A deploy will not start

POST /api/v1/deploys/:id/start re-runs the image a stopped deploy kept. It
refuses in three ways:

| Code | Meaning |
| --- | --- |
| image_reclaimed (409) | The image is gone. Only stopped deploys keep one; retention eventually moves an old deploy to deleted and the sweep takes it. Deploy the project again. |
| not_stopped (409) | The deploy is running, failed, mid-build, or another start already claimed it. |
| start_failed (502) | The container did not come up or did not answer on its port. Read the deploy logs. |

A deploy stuck at provisioning with nothing building is a leaked start claim:
the handler moves the row there before calling the runtime and releases it on
failure. Nothing sweeps that status, so if it persists, check the logs for
"failed to release a restart claim" and reset the row to stopped by hand.

Check whether the artifact is still there before assuming a bug:

~~~bash
sqlite3 /var/snaphost/data/snaphost.db \
  "select status, image_ref, image_deleted_at from deploys where id = '<uuid>';"
docker images | grep snaphost/proj
~~~

## SQLite and backup failures

Do not copy the live WAL database file. Use the documented .dump path. For lock
or corruption errors, stop write traffic, preserve the database plus -wal and
-shm files for diagnosis, and follow [backup and restore](backups.md).

## Production deployment failure

Read in-progress.env, current.env and previous.env before retrying. A migration
that started changes the rollback decision. See [rollback](rollback.md) and
[CI and deployment](ci-cd.md).
