# ADR 0004 — Keep runtime lifecycle behind an interface

Status: Accepted, reduced to the local runtime boundary
Date: 2026-05-10
Updated: 2026-08-30

## Context

The upstream SaaS supported local Docker and a Yandex cloud runtime. Provider
logic was kept out of deploy and saga orchestration so either backend could
create, probe, stop and inspect a runtime.

snaphost-self deliberately removed the cloud runtime. Docker on the same host is
the only supported backend.

## Decision

Keep the runtime backend interface between orchestration and Docker even with a
single implementation. Core deploy and saga code should depend on lifecycle
operations and results, not directly on Docker client types.

Do not preserve provider abstraction where the underlying concern disappeared.
The registry provider boundary was removed because images never leave the host,
and cloud credentials or build tags are not supported configuration.

RUNNER_BACKEND accepts only docker. Any other value is a startup error rather
than an unused compatibility option.

## Consequences

- Docker-specific labels, networks and hardening stay inside
  internal/runtime/backend/docker.
- Runtime probing and cleanup remain testable with fakes.
- Removing the Yandex implementation did not require rewriting saga logic,
  validating the original boundary.
- A future backend is not assumed to fit automatically. The interface must be
  reviewed against its actual image transport, addressing, probing and cleanup
  requirements before another implementation is added.
