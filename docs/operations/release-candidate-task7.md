# Task 7 release candidates

Status: Prepared locally; **not published**
Updated: 2026-10-05

These two proposed experimental versions provide a real upgrade and rollback
pair. Publishing them makes Git tags and GHCR images publicly available;
it does not itself deploy an operator's host. Task 7 remains In progress.

## Proposed versions

| Version | Source | Changes |
| --- | --- | --- |
| `v0.1.0` | `2ccf9453a1e28a2bc22aeba59fe76bc5ec3ddc40` | Task 4 runtime and existing operator CLI, corrected Linux Caddy-test cleanup, tested release tag resolution, OCI source/revision labels and reproducible Node drill. |
| `v0.1.1` | Subsequent Task 7 preparation commit | Initial BuildKit RAM sizing, explicit limit preservation, restore-runbook correction and recorded 1 GiB/VDS evidence. |

Both versions use SQLite schema 2 and the same application source. There is
no new DB migration between them. Before authorizing rollback, still verify
the actual image revisions and schema, and set
`MIGRATIONS_BACKWARD_COMPATIBLE=true` explicitly; the deploy engine correctly
does not infer compatibility from a version number.

`v0.1.0` retains the old 4 GiB default BuildKit ceiling. For its small-VDS
rehearsal, prepare a protected advanced env with `BUILDKIT_MEMORY_LIMIT=768M`
before installing. `v0.1.1` calculates an omitted limit from host RAM; it
preserves that explicit 768 MiB choice on upgrade. A fresh 1.9 GiB installation
of `v0.1.1` derives 960 MiB. The isolated 1 GiB guest derived 448 MiB.

## Verification completed before publication

- Go build, vet, tests and gofmt; golangci-lint 1.64.8 built with Go 1.25
  inside a container;
- frozen panel install, 47 Vitest tests, ESLint, Prettier and production build;
- deploy suite: 62 scenarios; backup suite: 41; operator suite: 35;
- release tag suite: main and strict SemVer, invalid versions rejected before
  writing output, mixed-case GitHub repository normalized for GHCR;
- ShellCheck, production Compose rendering, systemd installed-unit parsing,
  Caddyfile validation by the pinned Caddy 2.10.2 image;
- actual Caddy TLS/routing/state-reuse test on macOS and as a non-root Linux
  Docker operator, including complete cleanup;
- [1 GiB arm64 drill](rehearsals/2026-10-05-task7-1g.md): fresh supported-layout
  install, React build in 9.7 seconds without OOM or swap, encrypted scheduled
  SQLite backup and live restore, login/password rotation, DB permissions;
- [amd64 VDS drill](rehearsals/2026-10-05-task7-vds-node.md): pinned React build
  in 9.3 seconds, working HTML/JS, public control/project HTTPS still HTTP 200.

## Publication and remaining host drill

After approval to publish, push the candidate commits to `main` and wait for
CI. Publish immutable tags at their exact commits using the
[release procedure](ci-cd.md#publishing-a-release). Confirm anonymous pulls,
OCI revisions matching Git, and image digests for both versions. Package
visibility must be public; operator installs must not receive registry tokens.

Then preserve the existing VDS rehearsal's DB, protected env and Caddy state
before switching its public edge to the supported `/opt/snaphost` install:

1. install `v0.1.0` from public GitHub/GHCR, sign in and change the bootstrap
   password; check health, project deployment and backup timer;
2. run `snaphostctl upgrade v0.1.1`; verify Git/env/state/image alignment,
   account/projects and certificate fingerprints;
3. run `snaphostctl rollback --dry-run`, check that it is read-only, then
   perform compatibility-confirmed rollback to `v0.1.0` and verify the same
   invariants;
4. return to `v0.1.1` and record actual commands/results and image digests.

The 1 GiB drill used arm64 and a local registry; public CI images target amd64.
It establishes the measured workload, not an unrestricted 1 GiB production
guarantee. Large dependency graphs can still exceed the build budget. Preview
TTL, absence of persistent project volumes and managed databases remain
product limitations. Local encrypted backups do not protect against losing
the whole VPS; external storage remains an operator choice.
