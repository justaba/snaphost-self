# Custom domains

Status: Partially implemented; control-plane verification and aliases are
current, production Docker/TLS routing is not complete
Type: Operations
Updated: 2026-08-30

The application can register a domain, prove ownership through DNS, keep its
verification state and point it at a running deploy. The portable edge that
serves that mapping is still future work.

## Enable attachment

At least one stable public target must be configured:

- DOMAIN_CNAME_TARGET for subdomains;
- DOMAIN_A_RECORD_TARGET for apex domains.

When both are empty, POST /api/v1/domains returns 503
edge_address_unreserved. This is the default local-development behavior because
the repository does not reserve a public address for the operator.

RESERVED_DOMAINS must include the control-plane domain and DOMAIN_SUFFIX is
always reserved implicitly. MAX_DOMAINS_PER_USER and
DOMAIN_ATTACH_PER_HOUR bound attachment attempts. Do not enable
DOMAIN_ATTACH_REQUIRE_IDENTITY: no payment or external identity signal exists
in this self-hosted fork, so enabling it intentionally refuses every attach.

## Attach and verify

Attach by project:

~~~json
POST /api/v1/domains
{
  "domain": "app.example.com",
  "project_id": "<uuid>"
}
~~~

A deploy_id may be supplied instead; it selects that deploy as the initial
target and derives its project. Exactly one target form is required.

The response includes the TXT challenge and configured CNAME/A instructions.
Create the returned _snaphost-verify.<hostname> TXT record and point application
traffic at the configured stable edge.

The in-process verifier checks pending rows every
DOMAIN_VERIFY_INTERVAL_SEC. Verified rows are periodically rechecked according
to DOMAIN_REVERIFY_HOURS and the grace policy. Only verified domains may be
published by the future edge.

List the row through GET /api/v1/domains to see status and last_error. Common
verification errors are txt_not_found, txt_mismatch and dns_lookup_failed.

## Publish and rollback

After a deploy passes its runtime probe, the saga best-effort moves every
verified domain of that project to the new deploy. A promotion failure leaves
the domain on the prior working deploy and logs a warning.

Manual publish or rollback is the same atomic pointer update:

~~~json
POST /api/v1/domains/<domain-id>/target
{
  "deploy_id": "<running-deploy-uuid>"
}
~~~

The target must be a running deploy in the same project.

Archive clients must reuse a stable project_key when creating deploys. Without
it, every archive is a new project and an existing domain cannot follow the new
build.

## Current edge gap

The current Docker backend emits Traefik labels only for the generated
DOMAIN_SUFFIX hostname; it does not add custom-domain labels. The application
exposes no internal route lookup or TLS authorization API: those endpoints were
transport leftovers from the pre-collapse architecture, not a working Caddy
integration. Therefore infra/Caddyfile.production.example is a comments-only
marker for the missing contract, not an installable configuration.

Task 4 must provide both parts together:

1. a narrow, fail-closed TLS authorization contract compatible with Caddy ask;
2. dynamic routing from a verified host to the stored Docker container without
   exposing arbitrary internal routes.

Until then, custom-domain data and alias APIs can be tested, but custom domains
are not an end-to-end production feature of snaphost-self.

## Detach

DELETE /api/v1/domains/<id> revokes the row. Future routing and TLS
authorization must then fail closed. Any certificate already cached by an external
edge remains an edge-operator cleanup concern.

See [ADR 0007](../decisions/0007-custom-domain-tls-edge.md) and
[deployment model](../architecture/deployment-model.md).
