# Task 7 release record

Status: **Published and accepted**
Updated: 2026-10-05

These two experimental prereleases provide the tested install, upgrade and
rollback pair. Both Git tags and amd64 GHCR images are public. The
[acceptance report](rehearsals/2026-10-05-task7-public-release.md) records actual
anonymous pulls and host operations. Task 7 is complete.

## Published versions

| Version | Source | Changes |
| --- | --- | --- |
| `v0.1.0` | `2ccf9453a1e28a2bc22aeba59fe76bc5ec3ddc40` | Task 4 runtime and existing operator CLI, corrected Linux Caddy-test cleanup, tested release tag resolution, OCI source/revision labels and reproducible Node drill. |
| `v0.1.1` | `ccdbc6d797fcf12e8ae545e94e6972c996471286` | Initial BuildKit RAM sizing, explicit limit preservation, restore-runbook correction and recorded 1 GiB/VDS evidence. |

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

## Publication and accepted host drill

After explicit user approval, both commits were pushed to `main`; main CI and
both immutable tag workflows passed. The GitHub prereleases and public GHCR
images were published using the [release procedure](ci-cd.md#publishing-a-release).
Empty Docker configs proved anonymous pulls and OCI revisions matching Git.
Exact digests and workflow links are in the acceptance report.

The existing VDS rehearsal's DB, protected env and Caddy state were preserved
before switching to the supported `/opt/snaphost` installation. The drill passed:

1. public `v0.1.0` install, fresh login and password rotation, health, project
   recovery and active installed backup timer;
2. upgrade to `v0.1.1`, aligned Git/env/state/image, preserved account/projects
   and certificate fingerprints;
3. default rollback refusal, read-only compatibility-confirmed dry-run, then
   actual compatibility-confirmed rollback to `v0.1.0`;
4. return to `v0.1.1`, encrypted systemd backup, checksum/integrity and live DB
   replacement, successful login and trusted public control/project HTTPS.

The earlier 1 GiB arm64/local-registry drill was followed by a fresh 1 GiB
amd64 guest installation using the public `v0.1.1` image: React/Vite built
without swap/OOM, with 661.6 MiB peak host RAM and working compiled JS while
health and an existing site served. Public CI images target amd64. This
establishes a measured workload, not an unrestricted 1 GiB production
guarantee. Large dependency graphs can still exceed the build budget. Preview
TTL, absence of persistent project volumes and managed databases remain
product limitations. Local encrypted backups do not protect against losing
the whole VPS; external storage remains an operator choice.
