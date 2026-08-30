# Troubleshooting

Status: Current
Type: Operations
Updated: 2026-08-30

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

Transient build and scan retries are not implemented; retrying the deploy is
currently an operator action.

## Scan fails

Separate a critical vulnerability result from Trivy database, network or daemon
errors. SCAN_FAIL_ON_CRITICAL=true turns a critical finding into a permanent
build failure and removes the just-built image.

Trivy always runs at present even though the architectural decision is to make
it optional for operator-owned code.

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

The watchdog does not remove Docker images. Use docker system df to measure the
leak, but do not automate docker image prune until the product records a safe
image-retention policy.

## SQLite and backup failures

Do not copy the live WAL database file. Use the documented .dump path. For lock
or corruption errors, stop write traffic, preserve the database plus -wal and
-shm files for diagnosis, and follow [backup and restore](backups.md).

## Production deployment failure

Read in-progress.env, current.env and previous.env before retrying. A migration
that started changes the rollback decision. See [rollback](rollback.md) and
[CI and deployment](ci-cd.md).
