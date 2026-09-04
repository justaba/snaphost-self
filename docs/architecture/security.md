# Security model

Status: Current
Type: Architecture
Updated: 2026-09-01

snaphost-self has one trusted operator, but it still processes repositories,
Dockerfiles and dependencies that may be compromised. The application also has
root-equivalent access to the host Docker daemon, so the control plane is a
high-value boundary.

## Identity and authorization

- First startup creates one local admin account and logs a generated password
  once. No shared default password is shipped.
- Passwords use Argon2id. Sessions are opaque random values; only their SHA-256
  hashes are stored in SQLite.
- Browser sessions use an HttpOnly, SameSite=Lax cookie. SESSION_COOKIE_SECURE
  must be true behind production TLS.
- Non-browser clients use revocable sk_ API keys.
- Casbin authorizes each API route from the role stored in users.role.
- Login failures are limited per address in memory. Restarting the process
  clears the limiter.
- CORS restricts browser origins but is not an authentication control.

## Destructive operator actions

There is no separate admin surface. /api/v1/admin was removed along with the
console it fed: it existed so one role could read across every account, which is
a multi-tenant problem, and here the operator's own projects are all the
projects. Every path under it is refused by Casbin, and a test asserts that
absence rather than trusting the deletion.

DELETE /api/v1/projects/:id removes a project, its deploys, its sagas, its
domains, its containers and its images. Two properties make it admissible:

- host state is removed before the rows that name it, and a cleanup failure
  aborts with 502 having deleted nothing, because after the commit there is
  nothing left to find a stranded container or image with;
- an admin_audit_log row naming the operator is committed by the same
  transaction as the deletion, so the record cannot be lost while the effect
  survives. A request with no identifiable actor is refused with 401 rather
  than attributed to nobody.

Deletion is refused with 409 while any deploy or saga of the project is still
in flight. That check runs twice — once when the cleanup plan is loaded and
again inside the deleting transaction — because the build queue is in-process
and could otherwise create a container in the window between them.

The second check compares against the exact deploy set the cleanup acted on,
rather than filtering statuses. A build that completes during the cleanup
window ends at a status and a saga step that are both terminal, so a status
filter saw nothing wrong and deleted rows naming a container that was still
running. The cleanup window is up to five minutes.

The runtime call used here is the strict one. The ordinary stop reports success
for a deploy the runtime declines to recognise — which is what makes saga
compensation safe to repeat — and the Docker backend used to swallow a failed
container removal on top of that. Both now surface, because this is the caller
that destroys the only record of what it was stopping. The per-deploy network
is reported the same way and for the same reason: its name derives from the
deploy id, so once the rows are gone nothing can reconstruct it. The network is
removed even when the container is already absent, because that is the state a
second attempt finds after a first one removed the container and then failed.

The comparison against the planned set has a separate branch for an empty plan.
`id NOT IN (NULL)` evaluates to NULL in SQLite rather than TRUE, so the clause
would have matched nothing and a project with no deploys at plan time would
have accepted any deploy created during the window.

The collection path GET /api/v1/projects carries its own policy line. keyMatch2
does not let it inherit the item path's DELETE, and a test pins that:
addressing the list must not become a way to delete.

GET /api/v1/audit stays behind the admin role even though everything it records
is now on the user surface. Reading a record of destructive actions is a
different privilege from performing them.

The deletion is not recoverable from the panel: rows are hard-deleted rather
than marked, and the images are gone from the daemon. A SQLite backup restores
the records; it does not rebuild an image.

Components communicate through typed Go interfaces in one process. There is no
service-to-service HTTP surface and no shared webhook secret. `/internal/*`
paths are deliberately unrouted. A future TLS edge integration must introduce
its own narrow contract rather than reopening lifecycle, key-verification or
repository endpoints over HTTP.

## Source and build controls

- Git hosts are allowlisted. URL validation rejects private, loopback and cloud
  metadata destinations and pins the resolved address for the clone.
- Clone and archive workspaces enforce canonical path containment, size and
  timeout limits.
- Archive extraction rejects traversal and link escapes.
- User and generated Dockerfiles are validated against separate configured
  base-image policies; untagged and latest images are rejected.
- BuildKit is rootless and has its own resource limits and GC-managed cache
  volume.
- Short-lived Git credentials stay in process memory and are deleted after use.
- No vulnerability scanner is bundled or run during builds. This self-hosted
  installation treats submitted source and selected base images as code trusted
  by its operator; Dockerfile validation and base-image allowlists are policy
  controls, not vulnerability detection.

Build execution is not a strong sandbox against every BuildKit or kernel
vulnerability. Keep Docker, BuildKit, base images and the host patched, and do
not treat an allowlisted repository as trusted merely because its URL is
public. Operators that build untrusted third-party code should add scanning at
their own CI or image-admission boundary.

## Runtime controls

Before starting a container, runtime verifies the owner, deploy state, expected
local image prefix and deploy-ID tag. Lifecycle operations use the container ID
stored by the control plane rather than an arbitrary client-supplied ID.

User containers receive:

- a read-only root filesystem;
- tmpfs mounts for writable runtime paths;
- all capabilities dropped except the small set required by common non-root
  images;
- no-new-privileges;
- PID, CPU and memory limits;
- an isolated per-deploy network plus the shared routing network;
- a liveness probe before running is reported.

The shared routing network allows the control plane and edge to reach the
application port. It is not a tenant security boundary.

## Host boundary

The snaphost container mounts /var/run/docker.sock read-write. Membership in the
socket group is effectively root access to the host: a compromised control
plane can mount host files or start privileged containers. Running the process
as a non-root UID protects parts of its own filesystem but does not change this
fact.

Local Traefik also reads the Docker socket. The chosen Caddy direction removes
that second socket consumer, but its dynamic Docker routing is not implemented.

Protect OPENROUTER_API_KEY, backup credentials and the operator
session as host-level secrets. Do not commit infra/.env or production env files.

## Browser and edge boundary

Serve the control plane on a registrable domain separate from user deploys.
This prevents deployed code from setting cookies received by the operator
panel. Only verified custom domains may be published by the future TLS edge.

The current production edge is incomplete for the single-binary Docker model.
Do not rely on the retired cloud-router documentation.

## Known gaps

- a failed deploy cannot be retried from the panel; the only way forward is
  deploying the project again.
- Transient build failures have no retry budget.
- Caddy custom-domain routing and portable TLS installation are incomplete.
- Real build memory on a 1 GB host and a restore from encrypted off-host backup
  have not been proven.
