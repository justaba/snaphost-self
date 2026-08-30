# ADR 0002 — Scan after push with cleanup

Status: Superseded by the local-image pipeline
Date: 2026-05-16
Superseded: 2026-08-30

## Original context

The upstream builder pushed images to a registry before the runtime could use
them. Attempts to scan an OCI export directly were unreliable, so the accepted
path was push once, scan the registry reference and delete its manifest when a
critical vulnerability failed the gate.

## Why it no longer applies

snaphost-self builds and runs on one Docker host. Task 1 removed both the cloud
runtime and the registry. BuildKit now streams its Docker exporter into
ImageLoad on the host daemon.

## Current decision

After a successful load, Trivy scans the local image with the Docker image
source. When SCAN_FAIL_ON_CRITICAL is true and a critical finding is reported,
the build fails and the newly loaded image is removed from the daemon.

Trivy currently runs on every build. The product decision is to make scanning
optional by an explicit flag because this installation builds the operator's
own code, but that switch is not implemented yet.

## Consequences

- There is no network push, pull, registry credential or pre-scan exposure
  window.
- Runtime can inspect and start the exact local image without a registry.
- Trivy and Docker daemon failures remain part of the build path.
- Image cleanup on ordinary deploy deletion or expiry is still missing.
