# Deploy lifecycle

Status: Current
Type: Architecture
Updated: 2026-09-01

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

Images are named snaphost/proj-<owner-hash>:<deploy-id>. No push or pull occurs.

## Start and publish

6. Runtime verifies the deploy owner, expected image name, tag and state before
   creating a container.
7. The container receives resource limits, read-only root filesystem, tmpfs
   mounts, dropped capabilities, no-new-privileges and isolated networking. It
   also joins snaphost-net so the control plane and local edge can reach it.
8. Runtime waits for the container to stay up, then explicitly probes the
   detected application port. A started container that does not answer is
   stopped and the deploy fails.
9. Runtime stores the container identifier, endpoint, generated subdomain and
    expiry while moving the saga to provisioning in the same SQLite
    transaction. The saga then records terminal success and best-effort
    repoints verified custom-domain aliases for the project to the new deploy.

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
transient BuildKit and network failures is not implemented.

If the final running-state transaction fails, runtime treats it as transient,
stops the container and restores the deploy to building. Both persistence and
cleanup use bounded contexts independent of a disconnected caller, so request
cancellation cannot leave a successful side effect unrecorded merely by
cancelling the final database write.

Queues and live events are in memory. At process startup, sagas interrupted
during a build are rewound to pending, and a periodic sweeper resumes old
non-terminal rows. An uploaded archive or short-lived Git credential may no
longer exist after a restart; in that case the resumed deploy fails explicitly
rather than disappearing.

## Expiry and deletion

The watchdog finds expired or excess deploys through the control repository and
stops their stored container IDs. A deploy selected by a verified domain alias
is not reclaimed. Retention is controlled by DEPLOY_TTL_MIN,
DEPLOY_TTL_MAX_MIN and PROJECT_DEPLOY_RETENTION.

## Image reclamation

Every tick the watchdog also runs a second, independent sweep. It asks the
control repository for deploys at failed or deleted that still carry an
image_ref with no image_deleted_at, and removes each image from the local
daemon. There is no registry, so nothing else would ever collect them and the
disk that fills is the one the platform runs on.

stopped is deliberately excluded, because its image is what makes POST
/api/v1/deploys/:id/start a container run instead of a rebuild.

That exclusion needs an expiry or it is a leak. A third sweep moves stopped
deploys past STOPPED_IMAGE_GRACE_HOURS (default 168) to deleted, which is what
puts their images in the queue above. Without it nothing automatic ever leaves
stopped — the TTL and retention sweeps both end there, and the only other route
to deleted is an operator pressing the button — so every expired preview would
keep a container image for the life of the installation.

The grace runs from stopped_at, which SetRunning clears — that column records
the first stop and never moves on its own, so a restarted deploy would
otherwise be measured from a stop that happened before it last ran. A deploy an
alias publishes is never reclaimed.

The image is recorded on the deploy row the moment the daemon has it, before
anything else can fail the deploy. That column is the only thing the sweep
reads, so an image the database cannot name is one nothing will ever collect.

That write is a barrier holding one invariant: either the database names the
image, or the image is not on the host. It retries on a fresh bounded context,
because a cancelled build context is the likely reason the first attempt
failed, and if that fails too it removes the image and fails the build. It
cannot instead defer to a rebuild: a failed build is finalised as failed, never
retried, and a retry budget is unimplemented.

If the database and the daemon are both unreachable the image is orphaned. An
ERROR log line names it and the command to remove it; nothing durable can be
written in that state, because a deferred-cleanup record would need the
database that just refused.

## Stop, start and delete

Three different operations, and the distinction is load-bearing:

| Operation | Container | Image | Row |
| --- | --- | --- | --- |
| POST /deploys/:id/stop | removed | kept | stopped |
| POST /deploys/:id/start | created from the kept image | reused | running |
| DELETE /deploys/:id | removed | released | deleted |
| watchdog, after the grace | already gone | released | deleted |

Start does not go through the saga. The artifact exists, the port the build
detected is on the saga row, and the runtime's own liveness probe still decides
what running means, so the work is one container run — routing it through the
build queue would re-clone a repository to produce an image already on disk.

Start claims the deploy by moving it stopped to provisioning with a guarded
UPDATE before it calls the runtime. Two clicks would otherwise start two
containers for a row that records one. A refusal after the claim releases it;
nothing sweeps provisioning, so a leaked claim would leave the deploy looking
like it is starting indefinitely.

A deploy whose image the sweep reclaimed answers 409 image_reclaimed. That is a
refusal rather than an implicit rebuild: rebuilding has a different cost and
the operator should choose it. A failed deploy is never startable at all.

deploys.image_deleted_at is the durable marker. image_ref is kept after
cleanup for diagnostics, so the pair records both what was built and whether it
is still on disk. The marker is written only after Docker confirms the image is
absent, which makes a failure retryable and a success idempotent; a removal that
fails leaves the row queued for the next tick.

Images are also released eagerly on the paths that produce a dead deploy: a
failed liveness probe, deleting a deploy, and deleting a project. In each case
the row is recorded terminal before the image goes, because the terminal status
is what queues the watchdog's retry if the eager removal fails. The two
watchdog sweeps do not share a failure path, so a broken expiry query cannot
silently disable image reclamation.

The BuildKit cache volume is outside this deploy-row lifecycle. Its own daemon
runs periodic OCI-worker GC with a 512 MB reserved floor, a 4 GB maximum-used
target and a 5 GB host-free-space target. Those thresholds do not limit an
active build, and BuildKit cache records never substitute for the deploy images
that stop/start and rollback require.

The data model behind publishing is documented in
[projects, deploys and routing](deployment-model.md).
