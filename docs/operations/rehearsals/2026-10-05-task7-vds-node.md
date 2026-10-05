# Task 7 VDS Node build and release audit — 2026-10-05

Status: **Vite and pinned React/Vite 8 builds passed; public-release install remains untested**

The existing public-edge rehearsal on `31.177.109.37` was used without taking
down `snaphost.ru` or `kinocassa.ru`. This is still a locally built application
image, not a SemVer release install. At the start of the drill, public `git ls-remote` showed
`origin/main` at `e5af6771c4dd8118617192efd2a0e724422a60c1` and no version
tags. The local Task 4 commit was `ba311579be65b21521d40d0ba3c3cd9768d1726b`.
There was therefore no public release image to install or upgrade.

## Host and test application

- Ubuntu VDS, 2 vCPU, `MemTotal: 2009568 kB` (1962.5 MiB), no swap for the
  passing test;
- BuildKit cgroup: 768 MiB; control plane: 512 MiB; Caddy: 128 MiB;
- the published `kinocassa.ru` deployment B stayed running with a 256 MiB
  container limit;
- archive upload through the authenticated SnapHost API, containing a
  `node:22-alpine` Dockerfile and a Vite 6.3.5 `package.json`;
- Dockerfile ran `npm install --no-audit --no-fund` and `npm run build`, then
  started a non-root Node HTTP server on port 3000. AI generation was disabled.

The API returned deployment `8c091f88-1e45-4a90-bc3f-ec95a273a9ac` at
`2026-10-05T07:00:43.005Z` and reported it `running` at
`2026-10-05T07:01:08.557Z`: **25.6 seconds**. A 250 ms sampler read host
`MemAvailable` and the cgroup `memory.current` of BuildKit, the control plane,
Caddy and the existing site. The sample log is protected on the VDS at
`/root/snaphost-edge-rehearsal-20261004/evidence/task7-node-vite-memory.log`.

| Measure during this build | Observed peak or minimum |
| --- | ---: |
| Host RAM in use (`MemTotal - MemAvailable`) | 791.5 MiB peak |
| Host `MemAvailable` | 1171.0 MiB minimum |
| BuildKit cgroup | 524.6 MiB peak |
| Control plane cgroup | 108.0 MiB peak |
| Caddy cgroup | 48.5 MiB peak |
| Existing project B cgroup | 3.3 MiB peak |

`docker inspect` showed zero restarts and `OOMKilled=false` for the control
plane, Caddy, existing project B and the new Node deployment. During and after
the build, `https://snaphost.ru/health` returned HTTP 200 and
`https://kinocassa.ru/` returned HTTP 200 with deployment B's HTML. The two
temporary successful Node deployments were then stopped through the API;
the public project and control plane remained running. The simple test server
answered readiness requests but did not serve Vite's asset files, so this test
proves build and process startup rather than a working browser UI.

## React pressure probe

A second archive added React 19.1.0 and React DOM 19.1.0 to Vite. It did not
complete on this host:

| BuildKit limit and host swap | Result | Measured BuildKit peak | Host RAM in use peak |
| --- | --- | ---: | ---: |
| 768 MiB, no swap | `vite build` received SIGKILL; cgroup `oom_kill` rose by 1 | 768.0 MiB | 1302.5 MiB |
| 1 GiB, no swap | same result; cgroup `oom_kill` rose by 1 | 1024.0 MiB | 1521.1 MiB |
| 1.5 GiB, temporary 1 GiB swap | no OOM, but Vite stayed at `transforming...` for over five minutes; canceled | 1284.0 MiB | 1495.3 MiB |

The control plane, Caddy and existing project B had zero restarts and no OOM
kills through these probes. The temporary swap file was disabled and removed,
and the rehearsal BuildKit limit was returned to 768 MiB. The failed probes
show why a successful small Vite build is not a blanket memory guarantee for
Node projects with larger dependency graphs.

The supported installer previously left `BUILDKIT_MEMORY_LIMIT` at Compose's
4 GiB default even on a 2 GiB host. Task 7 now pins an initial limit at half
of host RAM, rounded down to 64 MiB and capped at 4 GiB, while preserving an
explicit operator setting. That prevents the default cgroup ceiling from
exceeding the small host's practical build budget. It does not make every
Node build fit, and it has not yet been exercised through a published install.

## Pinned React/Vite 8 retest

The checked-in [Node fixture](../../../infra/tests/fixtures/node-app/package.json)
uses React/React DOM 19.2.4, Vite 8.0.4, `npm ci` with a committed lockfile,
and the Node 22 Alpine image pinned by digest. Its runtime serves both HTML
and compiled JavaScript. This is a different dependency graph from the earlier
Vite 6 pressure probe, so those failures remain workload-specific evidence.

The [rehearsal runner](../../../infra/tests/node_build_rehearsal.py) was copied
under `/root/snaphost-edge-rehearsal-20261004/evidence` and run with:

```bash
cd /root/snaphost-edge-rehearsal-20261004
python3 evidence/node_build_rehearsal.py \
  --api-url https://snaphost.ru --email gorodslv@gmail.com \
  --password-file state/operator-password \
  --fixture evidence/node-fixture \
  --app snaphost-rehearsal-snaphost-1 \
  --builder snaphost-rehearsal-buildkitd-1 \
  --caddy snaphost-rehearsal-caddy-1 \
  --output evidence/task7-react-vite8.json
```

Deployment `4f86ccb1-25a4-4f0b-bfe0-328de81ea005` went from created at
`2026-10-05T11:50:31.077Z` to running at `2026-10-05T11:50:40.371Z`:
**9.3 seconds**, or 10.2 seconds including polling and asset verification.
There were 41 samples at 250 ms intervals.

| Measurement | Result |
| --- | ---: |
| Host RAM in use | 737.9 MiB peak |
| Swap in use | 0 |
| BuildKit cgroup (including retained cache from earlier probes) | 754.5 MiB peak |
| Control plane cgroup | 112.5 MiB peak |
| Caddy cgroup | 51.2 MiB peak |
| Existing baseline site cgroup | 10.4 MiB peak |
| Health and baseline-site probe pairs | 9 successful |
| OOM counter deltas and restarts | 0 |
| Node HTML and compiled React JavaScript | HTTP 200 |

The runner stopped its two temporary deployments. The original public project
stayed running, and final HTTPS probes returned `control_http=200`,
`project_http=200`, `tls_verify=0` for both. Caddy was not restarted by this
test; BuildKit remained limited to 768 MiB and host swap remained disabled.

## Remaining release acceptance

No public SemVer Git tag or public GHCR image existed at the time of this
audit, and the occupied 80/443 on this VDS belong to the current rehearsal.
A release must be published and anonymously pullable before a fresh operator
install, checkout-aware upgrade and rollback can be tested by the documented
path. The subsequent [1 GiB VM drill](2026-10-05-task7-1g.md) passed this same
React/Vite fixture and an encrypted scheduled SQLite backup/restore on the
supported layout. It used arm64 and a local registry/tag, so the public-release
acceptance remains open.
