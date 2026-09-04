# Architecture overview

Status: Current
Type: Architecture
Updated: 2026-09-04

snaphost-self builds a Git repository or uploaded archive into a Docker image
and runs it on the same Docker host. It is a single-operator application, not a
multi-tenant billing platform.

## Runtime shape

~~~text
browser / API client
        |
        v
  Compose Caddy edge
        |
        v
+---------------------- snaphost process ----------------------+
| Gin HTTP API and WebSocket logs                              |
| local sessions, API keys and Casbin RBAC                     |
| projects, deploys, custom domains and durable saga state     |
| build queue, project detection, Dockerfile generation        |
| Docker runtime, liveness probe and TTL watchdog              |
| embedded React operator panel and production edge adapters  |
+-----------------------+-------------------+-------------------+
                        |                   |
                        v                   v
                 rootless BuildKit     Docker daemon
                                            |
                                            v
                                      user containers
~~~

All application packages share one process and call each other through direct
adapters in internal/wiring. There is no internal HTTP hop for the normal build
or deploy flow.

External infrastructure is deliberately small:

- Docker owns images, networks and running user containers;
- BuildKit performs builds;
- local development uses Traefik to route generated hostnames;
- Compose Caddy terminates production TLS and calls separate proxy and
  certificate-authorization listeners over the internal control network.

PostgreSQL, Redis, a local image registry, Supabase, Terraform, billing and the
cloud runtime are not part of the current system.

## State

SQLite is the only durable application store. It runs in WAL mode at
DATABASE_PATH and contains users, sessions, API keys, projects, deploys, saga
state, domains, AI cache entries, AI usage records and the operator audit log.

The following state is intentionally process-local:

- build and saga queues;
- live build events and WebSocket fan-out;
- short-lived Git credentials;
- the in-memory login failure limiter.

Uploaded archives are staged as files, not held in memory. A restart loses
queued messages and credentials, but durable deploy_sagas rows are rewound and
resumed so interrupted work does not silently disappear.

Docker images and containers are durable outside SQLite. The database stores
their identifiers; the runtime verifies stored ownership before lifecycle
operations.

## HTTP boundary

The registration order in cmd/snaphost/main.go is part of the security
contract:

1. health, metrics and the self-authenticating WebSocket log route;
2. recovery, CORS, request ID, logging and the embedded panel;
3. session/API-key authentication, Casbin, identity enrichment and upload limit;
4. public API handlers under /api/v1.

There is no general service-to-service HTTP API. `/internal/*` remains unrouted
so a package boundary cannot accidentally become a privileged network
boundary. The production edge listeners are separate servers with only their
single-purpose handlers; they are reachable by Compose Caddy over the control
network and are not published on the host.

The panel middleware must run before authentication so the login page is
reachable. It never claims API, WebSocket, health or metrics paths, so
a missing API route remains a JSON 404 rather than an SPA response.

## Deployment topologies

Local Compose runs three services: snaphost, buildkitd and Traefik. The
application mounts the host Docker socket and joins the shared snaphost-net
network used by deployed containers.

The current production manifest runs snaphost, buildkitd and pinned Caddy. Caddy
is the only service publishing 80/443; the application publishes only a
loopback recovery port and exposes its edge listeners inside the control
network.
`infra/snaphostctl` installs it from `/opt/snaphost`, checks out release tags
for upgrades and keeps runtime, state and checkout aligned on rollback. Task 7
still has real-host acceptance work; Task 4 owns the generated-certificate
policy and public DNS/ACME proof.

See [packages and external components](services.md),
[deploy lifecycle](deploy-lifecycle.md), and [security](security.md).
