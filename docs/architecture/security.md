# Security model

Status: Current
Type: Architecture
Updated: 2026-08-30

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

Internal HTTP routes require WEBHOOK_SECRET using constant-time comparison and
are registered before user authentication. Most package-to-package calls are
now direct and never traverse those routes. The TLS authorization handler is
currently inside this protected group too. Standard Caddy ask cannot add the
secret header, so the edge integration is incomplete; making that path usable
requires a narrowly scoped loopback adapter or an equally explicit boundary,
not exposing all internal routes.

## Source and build controls

- Git hosts are allowlisted. URL validation rejects private, loopback and cloud
  metadata destinations and pins the resolved address for the clone.
- Clone and archive workspaces enforce canonical path containment, size and
  timeout limits.
- Archive extraction rejects traversal and link escapes.
- User and generated Dockerfiles are validated against separate configured
  base-image policies; untagged and latest images are rejected.
- BuildKit is rootless and has its own resource limits and cache volume.
- Short-lived Git credentials stay in process memory and are deleted after use.
- Trivy scans the local image. SCAN_FAIL_ON_CRITICAL currently defaults to true;
  on a gated critical result the image is removed.

Build execution is not a strong sandbox against every BuildKit or kernel
vulnerability. Keep Docker, BuildKit, Trivy and the host patched, and do not
treat an allowlisted repository as trusted merely because its URL is public.

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

Protect WEBHOOK_SECRET, OPENROUTER_API_KEY, backup credentials and the operator
session as host-level secrets. Do not commit infra/.env or production env files.

## Browser and edge boundary

Serve the control plane on a registrable domain separate from user deploys.
This prevents deployed code from setting cookies received by the operator
panel. Only verified custom domains may pass the TLS authorization gate.

The current production edge is incomplete for the single-binary Docker model.
Do not rely on the inherited router-svc or cloud-gateway documentation.

## Known gaps

- Docker images are not reclaimed after deploy deletion or expiry.
- Transient build failures have no retry budget.
- Caddy custom-domain routing and portable TLS installation are incomplete.
- Real build memory on a 1 GB host and a restore from encrypted off-host backup
  have not been proven.
