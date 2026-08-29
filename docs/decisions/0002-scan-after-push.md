# ADR 0002 — Scan after push with cleanup

Status: Accepted
Date: 2026-05-16

## Context

The builder must prevent critically vulnerable images from remaining usable.
Scanning a local OCI export before push was preferable in theory, but five
implementation attempts exposed unstable BuildKit filesync and OCI/Trivy
integration behavior.

## Decision

Build and push once, scan the registry image, and delete it through a registry
client when Trivy reports critical vulnerabilities and the gate is enabled.
The local Docker Registry implementation resolves a manifest digest and deletes
by digest.

## Consequences

- The pipeline is operationally simpler and uses normal registry behavior.
- A short image-existence window remains during scanning.
- Runner validation prevents an image from being deployed before the owning
  deploy reaches the correct state.
- Yandex Registry deletion remains a separate backlog implementation.

Detailed experiments are preserved in the implementation archive.
