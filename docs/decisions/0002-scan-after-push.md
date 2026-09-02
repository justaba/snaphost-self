# ADR 0002 — Scan after push with cleanup

Status: Superseded by ADR 0003
Date: 2026-05-16
Superseded: 2026-09-01

## Original context

The upstream builder pushed images to a registry before the runtime could use
them. Attempts to scan an OCI export directly were unreliable, so the accepted
path was push once, scan the registry reference and delete its manifest when a
critical vulnerability failed the gate.

## Why it no longer applies

snaphost-self builds and runs on one Docker host. Task 1 removed both the cloud
runtime and the registry. BuildKit now streams its Docker exporter into
ImageLoad on the host daemon.

## Former local-image follow-up

After a successful load, Trivy scans the local image with the Docker image
source. When SCAN_FAIL_ON_CRITICAL is true and a critical finding is reported,
the build fails and the newly loaded image is removed from the daemon.

This follow-up was removed by [ADR 0003](0003-remove-embedded-scanning.md).
There is no scanner or vulnerability gate in the current build path.

## Consequences

- There is no network push, pull, registry credential or pre-scan exposure
  window.
- Runtime can inspect and start the exact local image without a registry.
- At the time, Trivy and Docker daemon failures remained part of the build path.
- The host daemon became the only image store, so ordinary deploy expiry and
  deletion had to learn to remove images too. That is implemented: a terminal
  deploy's image is released and deploys.image_deleted_at records it. See
  [deploy lifecycle](../architecture/deploy-lifecycle.md). The BuildKit cache
  volume remains unmanaged.
