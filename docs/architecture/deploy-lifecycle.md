# Deploy lifecycle

Status: Current
Type: Architecture
Updated: 2026-08-30

## Create and build

1. An authenticated operator uploads an archive or submits a Git source to
   POST /api/v1/deploys.
2. Control resolves or creates the permanent project, then writes both the
   deploy and deploy_sagas rows before placing work on the in-process saga
   queue.
3. The saga enqueues one build job. Git sources are validated against the host
   allowlist and private or metadata addresses; archives are unpacked with path
   containment and size limits.
4. Builder detects the project. A built-in Dockerfile template wins when it
   matches; OpenRouter is used only as a fallback.
5. The Dockerfile is validated against the configured base-image policy.
   BuildKit builds it and streams Docker exporter output into ImageLoad on the
   host daemon.
6. Trivy scans the local image. With SCAN_FAIL_ON_CRITICAL enabled, critical
   findings fail the build and the newly loaded image is removed.

Images are named snaphost/proj-<owner-hash>:<deploy-id>. No push or pull occurs.

## Start and publish

7. Runtime verifies the deploy owner, expected image name, tag and state before
   creating a container.
8. The container receives resource limits, read-only root filesystem, tmpfs
   mounts, dropped capabilities, no-new-privileges and isolated networking. It
   also joins snaphost-net so the control plane and local edge can reach it.
9. Runtime waits for the container to stay up, then explicitly probes the
   detected application port. A started container that does not answer is
   stopped and the deploy fails.
10. Runtime stores the container identifier, endpoint, generated subdomain and
    expiry. The saga records terminal success and best-effort repoints verified
    custom-domain aliases for the project to the new deploy.

The alias move occurs only after the probe succeeds, so a failed build or
startup leaves the previously published deploy selected.

## State machines

deploys.status is the user-visible lifecycle:

~~~text
pending -> building -> provisioning -> running
   \           \            \
    +-----------+------------+-> failed
running -> stopped
~~~

deploy_sagas.current_step is the durable orchestration state:

~~~text
pending -> building -> built -> provisioning -> running
                              \-> compensating -> compensated
                               \----------------> failed
~~~

Built exists only in saga state. It is not a public deploy status.

## Failure and restart behavior

Permanent failures compensate immediately: a partially started container is
removed and the deploy receives a user-visible reason. A retry budget for
transient BuildKit, network and Trivy failures is not implemented.

Queues and live events are in memory. At process startup, sagas interrupted
during a build are rewound to pending, and a periodic sweeper resumes old
non-terminal rows. An uploaded archive or short-lived Git credential may no
longer exist after a restart; in that case the resumed deploy fails explicitly
rather than disappearing.

## Expiry and deletion

The watchdog finds expired or excess deploys through the control repository and
stops their stored container IDs. A deploy selected by a verified domain alias
is not reclaimed. Retention and idle-alias rules are controlled by
DEPLOY_TTL_MIN, DEPLOY_TTL_MAX_MIN, ALIAS_IDLE_GC_DAYS and
PROJECT_DEPLOY_RETENTION.

Stopping or deleting a deploy does not currently remove its Docker image. Image
garbage collection is a known disk-growth gap.

The data model behind publishing is documented in
[projects, deploys and routing](deployment-model.md).
