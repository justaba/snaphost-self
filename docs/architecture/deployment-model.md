# Projects, deploys and routing

Status: Current
Type: Architecture
Updated: 2026-08-30

## Data model

Publishing is split into three layers:

| Layer | Lifetime | Role |
| --- | --- | --- |
| Project | permanent | Owner-scoped identity for one source and the settings or domains that survive a build. |
| Deploy | immutable result | One build, image, container and generated hostname. |
| Custom domain | permanent mutable alias | A verified hostname pointing to one running deploy in its project. |

Git deploys derive a stable source key from repository and branch. Archive
deploys need a client-supplied project_key to converge on an existing project;
without one, each uploaded archive creates a separate project.

Publishing a new version and rolling back are both pointer updates. Moving a
domain alias does not rebuild or restart either deploy.

## Generated hostnames

Every deploy receives a unique subdomain under DOMAIN_SUFFIX. The Docker
runtime adds routing labels to the container and attaches it to snaphost-net.
Local Traefik reads those labels and forwards the generated host to the
container port.

The generated subdomain resolves only when the deploy is running and has a
stored container ID and endpoint. Local DNS for arbitrary subdomains is an
operator or workstation responsibility; localhost behavior varies by resolver.

## Custom domains

A domain is attached to a project, not directly to a build. Attach returns:

- a TXT ownership challenge under _snaphost-verify.<hostname>;
- the configured CNAME and/or A target.

An in-process verifier changes pending to verified only after the TXT value
matches, and periodically rechecks ownership. Unknown, pending, failed or
revoked domains never route.

GET /internal/tls/authorize contains the fail-closed verification lookup needed
by a future Caddy edge, but it currently requires WEBHOOK_SECRET and standard
Caddy ask cannot send that header. The single-binary deployment also lacks the
dynamic Caddy-to-Docker routing layer. The removed router-svc example has been
replaced by a comments-only warning rather than a fictional working config.

## Browser isolation

The operator panel and deployed code should use different registrable domains
in production. Otherwise a deployed application may set a parent-domain cookie
that the panel receives. Origin-scoped storage such as localStorage is already
isolated per hostname, but cookies are scoped by registrable domain.

A production edge should therefore provide:

- a control-plane domain for the panel and API;
- a separate deploy suffix for generated sites;
- preferably a Public Suffix List entry for that deploy suffix, or another
  mechanism that prevents one generated site setting cookies for its siblings.

This remains a deployment requirement, not something the current Compose files
automate.

## Local and production placement

Local development:

~~~text
host ports -> snaphost / Traefik
snaphost -> BuildKit
snaphost -> host Docker socket -> user containers
all routing participants -> snaphost-net
SQLite -> named snaphost_data volume
~~~

The production manifest contains snaphost, the one-shot migration profile and
BuildKit. It pins one application image to a 40-character Git SHA and leaves TLS
and public routing to the host. It is an environment-specific deployment
artifact, not yet a supported installer.

Task 2 changes preview TTLs into opt-in expiry and adds persistent per-project
configuration. Task 4 completes the Caddy edge. Task 7 defines portable install,
upgrade and rollback.
