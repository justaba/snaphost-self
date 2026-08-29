# Task 1 — Collapse the control plane into one binary

**Status:** In progress. The cloud runtime path, the dead documentation, and
billing are removed; the module merge, SQLite, and the in-process queue remain.
**Created:** 2026-08-29
**Updated:** 2026-08-29

## Goal

Run the whole platform — panel, build pipeline, runtime control, and edge — on
the cheapest VPS tier a provider sells, and leave most of that machine's memory
to the sites it hosts.

The target is **under ~170 MB resident with nothing deployed**, against roughly
500–600 MB for the inherited stack. The number that matters is not the platform's
own footprint but what is left over: on a 1 GB box, 150 MB of platform leaves
800 MB for user containers, and 550 MB leaves almost nothing.

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
4. [ ] Merge six Go modules into one, with the services becoming packages under
   `internal/`. Mechanical but wide: every import path changes.
5. [ ] One `main` wiring those packages together. The saga's HTTP clients
   (`internal/saga/clients.go`) become interfaces satisfied by direct calls,
   which is what deletes the internal `X-Webhook-Secret` layer with them.
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

Before claiming any of this worked, take a baseline: bring the inherited stack
up and record `docker stats` idle and during a build. Every later claim about
memory is guesswork without it, and the whole task is justified by a number
nobody has measured yet.

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
