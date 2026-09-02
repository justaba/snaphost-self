# ADR 0003 — Remove embedded vulnerability scanning

Status: Accepted
Date: 2026-09-01

## Context

snaphost-self builds code selected by its own operator on a single host. The
embedded Trivy step treated every build as an untrusted image-admission event,
but it also added a large CLI and vulnerability database, reserved hundreds of
megabytes in the application cgroup, required network/database availability and
could block deployment for a check this trust model does not require.

## Decision

The application does not bundle or invoke a vulnerability scanner. A successful
BuildKit export is loaded into the local Docker daemon, recorded durably and
passed directly to runtime provisioning. There is no scan feature flag or
vulnerability-gate configuration to maintain.

Dockerfile validation, base-image allowlists and runtime hardening remain. They
are policy and containment controls, not substitutes for CVE detection.

## Consequences

- The runtime image is smaller and builds avoid scanner database downloads,
  scan latency and scanner-specific failure modes.
- The application cgroup no longer reserves memory for a scanner child process.
- snaphost-self does not detect vulnerable OS packages or dependencies in built
  images. Operators that build untrusted or third-party code must scan in their
  own CI or image-admission boundary.
- Image bookkeeping and watchdog reclamation remain unchanged; they protect
  local disk independently of why a deploy fails or expires.
