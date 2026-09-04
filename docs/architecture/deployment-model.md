# Projects, deploys and routing

Status: Current
Type: Architecture
Updated: 2026-09-04

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

The monolith exposes two dedicated edge listeners outside the operator API: a
Host-to-container reverse proxy and `/tls/ask` for Caddy's on-demand certificate
authorization. They are available only on the Compose control network, are not
published on the host, and do not form a general `/internal` service API. Caddy
receives no Docker socket.

The resolver accepts only a generated hostname or verified custom-domain alias
with a running deploy, recorded container and valid saga port. It constructs
the upstream from the deploy ID instead of accepting an arbitrary stored or
request-supplied URL. Alias moves are visible on the next request.

## Browser isolation

The operator panel and deployed code should use different registrable domains
in production. Otherwise a deployed application may set a parent-domain cookie
that the panel receives. Origin-scoped storage such as localStorage is already
isolated per hostname, but cookies are scoped by registrable domain.

The production edge must therefore provide:

- a control-plane domain for the panel and API;
- a separate deploy suffix for generated sites;
- preferably a Public Suffix List entry for that deploy suffix, or another
  mechanism that prevents one generated site setting cookies for its siblings.

This remains a deployment requirement. The installer requires both names and
conservatively refuses values that share their final two DNS labels.

## Local and production placement

Local development:

~~~text
host ports -> snaphost / Traefik
snaphost -> BuildKit
snaphost -> host Docker socket -> user containers
all routing participants -> snaphost-net
SQLite -> named snaphost_data volume
~~~

The production manifest contains snaphost, Caddy, the one-shot migration
profile and BuildKit. It pins the application image to a
`vMAJOR.MINOR.PATCH` tag. Caddy alone publishes host ports 80/443 and reaches
the two application edge listeners over the internal control network.
`infra/snaphostctl` installs the Compose manifest
from a tag checkout at `/opt/snaphost` and keeps checkout, env and state on the
same release during upgrade and rollback.

Task 2 changes preview TTLs into opt-in expiry and adds persistent per-project
configuration. Task 4 defines the generated-host certificate policy and proves
the public DNS/ACME path. Task 7 still owns the remaining real-host proof of the release,
upgrade and rollback contract.
