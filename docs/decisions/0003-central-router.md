# ADR 0003 — Central router for Yandex runtime

Status: Accepted
Date: 2026-05-28

## Context

Yandex API Gateway routes by OpenAPI path, not by arbitrary per-operation Host
metadata. Mutating one shared greedy route per deploy cannot safely serve two
subdomains concurrently and exposes update races.

## Decision

Terraform owns one static wildcard API Gateway route to `router-svc`. Runner
creates/deletes user Serverless Containers without changing the gateway.
Router validates the host, resolves the running deploy through billing, obtains
Yandex invocation credentials, and proxies to the target container.

## Consequences

- Concurrent subdomains were proven with two live Yandex containers.
- Gateway configuration becomes stable and Terraform-owned.
- Every request currently performs route lookup; caching is future work.
- End-user Authorization forwarding, WebSocket, large upload, and long-stream
  support remain outside the MVP.
