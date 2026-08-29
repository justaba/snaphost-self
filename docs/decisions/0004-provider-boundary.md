# ADR 0004 — Provider boundary

Status: Accepted
Date: 2026-05-10

## Context

Yandex is the first production provider, but SnapHost must retain a working
local Docker runtime and allow future providers without rewriting billing,
builder, or saga logic.

## Decision

Cloud lifecycle behavior stays behind the runner backend interface. Registry
operations use a provider boundary. Yandex runner code is isolated with the
`yandex` build tag. Configuration selects providers; core services do not
hardcode Yandex registry hosts or resource IDs.

## Consequences

- Default Docker builds remain independent of Yandex compilation.
- Production runner images must explicitly enable the Yandex build tag.
- Provider-specific limitations remain inside adapters.
- The backend interface requires an audit before adding a second production
  provider because fields such as subdomain may encode current assumptions.
