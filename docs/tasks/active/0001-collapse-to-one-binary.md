# Task 1 — Collapse the control plane into one binary

**Status:** In progress. The cloud runtime path, the dead documentation, and
billing are removed, the six modules are one, and the platform is a single
binary; SQLite, the in-process queue and GOMEMLIMIT remain.
**Created:** 2026-08-29
**Updated:** 2026-08-29

## Goal

Run the whole platform — panel, build pipeline, runtime control, and edge — on
the cheapest VPS tier a provider sells, and leave most of that machine's memory
to the sites it hosts.

**Measured 2026-08-29, and the estimate this task was written on was wrong.**
The inherited stack was guessed at 500–600 MB. It is **131 MiB** across twelve
containers. Go services idle at 3–13 MiB each, not the 20–40 MiB assumed.

| | inherited | after the collapse |
| --- | --- | --- |
| containers | 12 | 6 |
| application | 40.5 MiB across 7 | **8.9 MiB in 1** |
| PostgreSQL | 25.7 | 25.7 |
| Redis | 17.8 | 17.8 |
| BuildKit | 22.1 | 15.6 |
| Traefik | 20.0 | 21.4 |
| registry | 5.2 | 5.2 |
| **total idle** | **131.1 MiB** | **96.8 MiB** |

So the ~170 MB target was already met before any of this work, and the headline
justification was overstated by roughly four times. What the measurement does
support is narrower and still real: the application side went from 40.5 MiB
across seven processes to 8.9 MiB in one, and six containers stopped existing.

Items 6 and 7 are where the rest is: PostgreSQL (25.7) and Redis (17.8) are
together more than four times the whole application, and the registry (5.2)
goes with them. That projects to roughly **50 MiB** — the binary, BuildKit and
an edge — which is the number worth aiming at now.

The figure that matters is still what is left over rather than what the
platform uses: on a 1 GB box, 50 MiB of platform leaves 950 MiB for the sites.

## Why this is deletion, not optimisation

The inherited code is efficient Go. There is nothing to win by tightening
handlers. The waste is structural:

- **twelve containers** where one process would do. The microservice split
  exists for multi-tenant SaaS concerns — independent scaling, per-role cloud
  credentials, a shared-secret boundary between services. A single operator on
  a single host has none of those, and pays for all of them: seven Go runtimes,
  seven health checks, and JSON serialisation between components that could
  call each other directly.
- **PostgreSQL and Redis as separate daemons** for a workload with one writer.
- **A local registry and a BuildKit daemon** to move an image between two
  processes on the same machine.

Coolify is PHP/Laravel and Dokploy is Node; a large part of their footprint is
runtime and framework. A single static Go binary starts from a different place.
That advantage is only real if it is actually one binary.

## Decisions

Settled 2026-08-29, owner decision:

- **Builds run on the box.** Not on CI, not on a build host. This is the
  expensive choice and it was made deliberately: it keeps "point at a repo, get
  a site" true on one machine. The consequences are accepted and have to be
  designed for — build concurrency of one, a memory ceiling on the build step,
  and swap sized so a Node build degrades instead of OOM-killing a running site.
- **SQLite, not PostgreSQL.** One writer, one operator, WAL mode. Only 13 lines
  of Postgres-specific SQL exist across all inherited migrations, so the port is
  bounded. A backup becomes copying one file.
- **No Redis.** Its five jobs move in-process: the build queue becomes a channel
  plus a durable table, log pub/sub becomes direct fan-out to WebSocket
  subscribers, git credentials become a map with a TTL, and rate limiting stops
  being a concept. Uploaded archives move to a temp file — today a 50 MB tarball
  is held in RAM, which on this class of machine costs more than the entire Go
  runtime it is trying to save.
- **No registry.** The Docker backend runs images out of the host's own image
  store. Push and pull existed only to reach a cloud runtime.
- **Trivy off by default.** It defends against untrusted code. On a self-hosted
  platform the code is the operator's own. Left behind a flag, not deleted.
- **Caddy, not Traefik.** Lighter, ACME built in, no Docker socket, and the
  inherited `Caddyfile` from the custom-domain edge already does most of it.

Not decided yet: whether to replace Docker with rootless Podman. It removes a
daemon worth more memory than everything above combined, and `bollard`-free Go
code already speaks a socket Podman can serve — but it needs measuring on a real
host before it becomes a plan.

## Work plan

1. [x] Remove the cloud runtime path: `terraform/`, `router-svc`, the Yandex and
   VK runtime backends, and every config reference to them.
2. [x] Remove documentation describing the SaaS this forked from, and archive
   the task catalog that explains code we kept ([../../inherited/](../../inherited/)).
3. [x] Remove billing: `wallet` and `transaction` packages, the reserve/commit/
   refund steps in the saga, `cost_vibecoins` and `reservation_tx_id`, the
   `/billing` routes, and the wallet views in the admin console.

   The saga itself stays. Stripping the money out does not make it pointless:
   compensation still has to tear down a half-created runtime and mark the
   deploy failed. It stops being a payment saga and goes back to being a
   distributed-work saga, which is what it always was underneath.

   Two things fell out of it that were not on this list:

   - **`pending` became a state a saga can sit in.** It used to be traversed
     instantly on the way to `reserved`; now it is the step before the first
     external call, so an enqueue that fails transiently leaves the saga there.
     The resume sweeper did not look for `pending` and now does — without that
     the deploy would have been stuck until someone noticed.
   - **The identity half of the wallet webhook had to survive.** `POST
     /internal/users` seeded a wallet *and* recorded the email, which is the
     only path an address ever takes into this database. It moved to a new
     `internal/account` package with the money removed.
4. [x] Merge six Go modules into one.

   **No import path changed**, which was not luck: the module paths were
   `snaphost/<service>` and the directories are `<service>/` under
   `snaphost-backend/`, so one module named `snaphost` rooted there resolves
   every existing path identically. The merge is one `go.mod`, six deleted, and
   the three `replace snaphost/shared => ../shared` directives gone.

   Moving the services to `internal/` is deliberately **not** part of this step.
   Renaming directories and merging modules in one commit would hide a real
   break inside a rename diff; the rename lands with item 5, where the packages
   are being rewired anyway.

   One dependency conflict had to be resolved by hand. `go mod tidy` on an empty
   require set went looking for `github.com/docker/docker/api/types/container`
   and found the split-out `github.com/docker/docker/api` module, which now
   declares itself as `github.com/moby/moby/api` and fails to resolve. Seeding
   the file with the highest version each module already pinned — `docker`
   at `v27.2.0+incompatible`, where those packages still live inside the main
   module — fixes it. Worth knowing before anyone runs `go get -u` here.

   Version bumps the merge forced, since MVS takes the maximum: gin 1.10 → 1.12
   (api-gateway and user-billing were behind shared), pgx 5.7.1, and the Go
   directive to 1.25.5. All 28 test packages pass on them.
   The last of the cloud path went with it: `shared/yandexauth` and the
   builder's IAM registry auth, which were missed in item 1 because they are
   *registry* credentials rather than runtime ones. `REGISTRY_AUTH_MODE` now
   accepts only `static`, and `RUNNER_BACKEND` only `docker` — the latter was
   still set to `yandex` in the production Compose file, which would have
   failed at startup rather than silently.

   **The deployment scripts and the production Compose file are now internally
   consistent but still the wrong shape.** They describe pushing GHCR images
   pinned to a Git SHA onto someone's VDS over SSH. A self-hosted product is
   installed by its operator, so `deploy.sh`, `deploy-remote.sh` and
   `docker-compose.prod.yml` need rewriting rather than editing — after item 5,
   when there is one image to ship instead of five. Until then they are kept
   working rather than left referencing services that no longer exist.

   The privilege model changed and is worth stating plainly: the builder and
   runner service-account keys are gone, and with them Task 12's guard that
   refused a key belonging to another environment. What replaced them is the
   Docker socket the runner mounts, which is root on the host and has no
   equivalent identity check.

5. [~] One `main` wiring those packages together.

   **5a — layout, done.** The services moved under `internal/` (`gateway`,
   `control`, `builder`, `runtime`, `ai`, `shared`), every entry point moved to
   `cmd/`, and the five Dockerfiles collected into `snaphost-backend/docker/`.
   Pure renaming: no behaviour changed, and the compiler verified every one of
   the ~150 files it touched.

   Two things the rename surfaced that predated it. `parseInt64Env` in the
   control config had been dead since billing was removed, and fourteen files
   had struct alignment left wrong by the same commit — both caught by running
   `golangci-lint` locally in a Go 1.25 container, which is what CI does and
   what had not been done here before. The published v1.64.8 binary cannot lint
   this module at all: it is built with go1.24 and refuses a module targeting
   1.25, exactly as the CI comment predicts.

   **5b — wiring, done.** `cmd/snaphost` is the whole platform. Seven entry
   points are gone; the two migrators stay, because a deployment may need to
   apply a schema change before the service that would otherwise do it at
   startup is allowed to run.

   Every cross-service HTTP client is now a direct call
   (`internal/wiring`), and the gateway no longer proxies — `internal/gateway/proxy`
   and its route table are deleted, and the control and generator handlers
   register on the same engine as the middleware chain.

   One ordering bug was found and fixed while assembling it. Registering the
   control routes after the middleware chain put the `/internal` group behind
   JWT and Casbin, so every secret-authenticated caller would have been
   rejected before reaching its secret check. `routes.Register` is now split
   from `routes.RegisterInternal`, and the internal half is registered before
   the chain. The separation used to be free, because those routes lived in a
   different process.

   The API-key path needed the same care: `middleware.JWT` took a concrete
   HTTP verifier, and passing `nil` would have made every `sk_` bearer fail
   with `api_key_unsupported` — silently breaking every non-browser client. It
   takes a `KeyVerifier` interface now, satisfied by a direct repository
   lookup.

   Costs worth stating rather than discovering later:

   - **The image got fatter, not thinner.** Four of the five could have been
     distroless; the build pipeline shells out to `git` and `trivy`, so the
     single image inherits the fattest base of the set.
   - **The rollout is no longer ordered.** `deploy.sh` used to update six
     services in dependency order and publish the gateway last, so a failure
     in between left the previous gateway serving. There is one container now:
     it restarts as a unit.
   - **A panic takes everything.** Recovery middleware covers the request
     path; the background loops do not have an equivalent yet.
6. [ ] Port the store to SQLite and squash twelve migrations into one baseline.
   There is no data to migrate — a fork starts empty — so the inherited
   migration history buys nothing and carries vibecoin columns forward.
7. [ ] Replace Redis with the in-process queue, pub/sub, and credential store.
8. [ ] Set `GOMEMLIMIT` and a container memory limit that agree with each other.
   Neither `GOMEMLIMIT` nor `GOGC` is set anywhere in the inherited tree, while
   the production Compose file does set container memory limits — so Go never
   learns about the ceiling it is running under and grows its heap until the
   kernel intervenes.
9. [ ] Rewrite `CLAUDE.md` and the architecture docs, which currently describe
   seven services and a cloud runtime that no longer exist.

## Measurement

Done 2026-08-29, and it changed the case for the task — see the table above.
The baseline was taken by running both stacks and reading `docker stats` idle.
What has still not been measured is either stack **during a build**, which is
the case that decides whether a 1 GB box is viable at all.

## Acceptance criteria

- One binary, one container, plus Caddy and the Docker daemon.
- Idle resident memory for the platform under ~170 MB, measured rather than
  estimated, against a recorded baseline.
- A Node project builds on a 1 GB instance without killing a running site.
- No vibecoin, wallet, Yandex, or Terraform reference remains in code or config.
- `make test` passes throughout; no step in this task lands with a red tree.

## Out of scope

Everything that makes this a product rather than a smaller SnapHost: persistent
volumes, per-project environment variables, managed databases and auth as
attachable resources, git webhooks, and operator actions. Those are Tasks 2
onward and they all assume this one has landed.
