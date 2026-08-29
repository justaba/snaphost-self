# Tasks 9–9.4 — Project detection and Dockerfile policy

Status: Completed
Period: May 2026

Delivered runtime-version detection, CRA compatibility heuristics, AI cache
schema versioning, one source of truth for base-image constraints, separate
strict/permissive Dockerfile policies, and canonical OCI image-reference
normalization.

Known follow-up: `.python-version` is detected but Python templates do not yet
consume it.

Implementation details and smoke cases are in
[`../archive/implementation-log.md`](../archive/implementation-log.md).
