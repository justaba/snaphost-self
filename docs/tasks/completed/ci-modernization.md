# CI modernization

Status: Completed
Date: 2026-07-03

The active workflow validates frontend, every Go module, Yandex-tagged runner,
Terraform, and all deployable backend images. Main-branch builds publish
immutable GHCR images and deploy the frontend. Broken backend SSH deployment
was removed until Task 11 provides a real production manifest and rollback
contract.

See [`../../operations/ci-cd.md`](../../operations/ci-cd.md).
