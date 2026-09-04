# ADR 0007 — Terminate custom-domain TLS at an operator-owned Caddy edge

Status: Accepted; post-collapse implementation incomplete
Date: 2026-08-04
Updated: 2026-08-30

## Context

A wildcard certificate for the platform's generated deploy suffix cannot cover
domains owned by an operator's users. Each custom hostname needs its own
certificate, and issuance must happen only after the TXT verification stored in
custom_domains succeeds.

The upstream implementation compared a cloud-gateway integration with Caddy on
the persistent host. It selected Caddy because the gateway certificate
challenge still required an HTTP edge, mutated Terraform-owned resources and
introduced provider-specific lifecycle work.

Task 1 later removed the cloud runtime and router-svc. The decision remains
useful, but its old implementation path does not.

## Decision

Terminate TLS for verified custom domains at an operator-owned Caddy edge using
on-demand ACME issuance guarded by a fail-closed authorization check.

The edge must satisfy two contracts:

1. certificate authorization returns success only for a normalized hostname
   whose custom_domains row is verified;
2. after TLS termination, dynamic routing resolves that hostname to the running
   Docker container stored for its alias target.

Neither contract may expose arbitrary internal control routes. A database or
control-plane error must refuse issuance rather than fail open.

## Current implementation state

The domain repository, TXT verifier and alias target exist.

The complete edge does not:

- the Docker backend emits Traefik labels only for generated hostnames;
- there is no dynamic Caddy-to-Docker route adapter;
- there is no Caddy-compatible TLS authorization contract;
- infra/Caddyfile.production.example is intentionally comments-only until the
  missing authorization and routing contract is implemented.

Task 4 must close these gaps before custom domains are advertised as an
end-to-end feature.

## Consequences

- One DNS traffic record plus the TXT ownership record is sufficient from the
  domain owner.
- ACME issuance state and private keys live on the edge and require an encrypted
  off-host backup.
- Caddy becomes a public, stateful prerequisite until Task 4 integrates it.
  Task 7's installer deliberately treats HTTPS and routing as an external host
  prerequisite rather than installing a knowingly incomplete edge.
- On-demand TLS without a working authorization gate is forbidden.
- Control-plane and deploy suffix should remain separate registrable domains so
  deployed code cannot set cookies received by the operator panel.
- A custom-domain edge failure must not fail a build or move an alias away from
  the previous running deploy.

## Alternatives

A certificate per domain in a cloud gateway is rejected: it reintroduces the
provider and Terraform lifecycle removed by this fork and still needs a viable
ownership challenge path.

Adding custom-domain labels directly to one deploy container is insufficient:
an alias can move between deploys and Caddy must authorize certificates before
a container is selected. Routing must consume the durable alias model.
