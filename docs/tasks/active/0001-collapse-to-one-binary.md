# Task 1 — Collapse the control plane into one binary

**Status:** In progress. The cloud runtime path, the dead documentation and
billing are removed, the six modules are one, the platform is a single binary
on SQLite, it issues its own identity, and Redis is gone. GOMEMLIMIT, the docs
rewrite and the embedded panel remain.
**Created:** 2026-08-29
**Updated:** 2026-08-30

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

After item 6 removed PostgreSQL: **56.1 MiB across five containers** — snaphost
9.6, Traefik 21.0, BuildKit 16.2, Redis 4.8, registry 4.5.

After item 6a, on a fresh stack and before anyone logs in: **48.1 MiB** —
snaphost 7.4, Traefik 17.0, BuildKit 15.5, registry 4.4, Redis 3.8. The
application half got smaller, because the JWKS cache and the JWT machinery went
and only a session lookup replaced them. Traefik and BuildKit vary by a few MiB
between runs, so the honest comparison is the application line.

**A login moves it to ~69 MiB and it stays there.** Hashing a password is a
7 MiB allocation, Go sizes the heap against it, and nothing gives it back —
snaphost sits at 28.8 afterwards and does not climb further no matter how many
times the operator logs in. Item 8 is what bounds this.

After item 7 removed Redis, on a fresh stack restarted so the bootstrap hash is
not in the sample: **44.7 MiB across four containers** — Traefik 17.2,
BuildKit 15.3, snaphost 8.0, registry 4.3.

The application did not get smaller, and that is the honest reading: 7.4 before,
8.0 after, which is inside the noise of two measurements. Everything Redis held
moved into this process, so the queues, the log history and the upload index are
now its memory rather than another container's — and it still came out level,
because what they replaced was a client library, four connection pools and a
serialisation step per message. The 3.8 MiB the Redis container itself used is
the whole saving, plus a daemon, a volume and an appendonly file with git
tokens in it.

So the ~170 MB target was already met before any of this work, and the headline
justification was overstated by roughly four times. What the measurement does
support is narrower and still real: the application side went from 40.5 MiB
across seven processes to 8.9 MiB in one, and six containers stopped existing.

The edge is what is left. Traefik at 17.2 is now the largest thing running,
and Caddy replaces it in Task 4; the registry at 4.3 goes with the decision
above that a build and the container running it share a host. The floor is
BuildKit at 15.3, and it only exists while something is being built.

The figure that matters is still what is left over rather than what the
platform uses: on a 1 GB box, 45 MiB of platform leaves nearly 980 MiB for the
sites.

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
- **No Redis.** Its jobs move in-process — eight of them, not the five counted
  here: the build queue becomes a channel
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

The numbers are identities, not an order — commits and other documents refer to
"item 6a" and "item 10", so they do not move. What remains is done in the order
**8 → 10 → 9**, which differs from the list below in one place and is argued at
[Order of the remaining items](#order-of-the-remaining-items).

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

   **The deployment scripts and the production Compose file are still the wrong
   shape.** They describe pushing GHCR images pinned to a Git SHA onto
   someone's VDS over SSH. A self-hosted product is installed by its operator,
   so `deploy.sh`, `deploy-remote.sh` and `docker-compose.prod.yml` want
   rewriting rather than editing. That rewrite is deferred, deliberately: it
   changes CI, the release model and the rollback story at once, and none of
   those is what Task 1 is about. It is
   [Task 7](../planned/0007-install-and-upgrade.md), and it is ordered after
   the rest of this task because items 7, 8 and 10 each touch the same files.

   What they were *not* was "kept working". Two defects were found on
   2026-08-29 by reading them after item 6a, and both had been live for
   several commits with a green test suite:

   - **Rollback could not run.** `rollback_to` iterated the seven services
     deleted in item 5, so it failed on its first `compose up`. The tests
     missed it because they fake `docker`, and a fake does not object to a
     service the manifest never had. The fake refuses unknown services now,
     and restoring the old list fails four tests.
   - **Every backup would have been refused.** `REQUIRED_TABLES` still named
     `wallets` and `transactions`, dropped with billing in item 3, so
     `verify_archive` looked for table data that could not exist. That test
     passed because its fixture was generated from the same list — a check and
     its fixture derived from one another agree with each other and nothing
     else.

   Both are fixed, along with the PostgreSQL-to-SQLite port those files never
   got: no `postgres` service, no `DATABASE_URL`, no credentials, and the dump
   is `sqlite3 .dump` inside the application container. Copying the volume is
   the obvious alternative and it is wrong under WAL. A third thing surfaced on
   the way: `RUN_MIGRATIONS=false` had been in the production env example since
   it was written and the Compose file never passed it, so the application
   migrated at startup anyway and the profile-only migrate service was
   decoration.

   Verified against the running stack rather than only against fakes: a dump
   taken from the live container, verified, checksummed, pruned and listed;
   restored into a fresh database with `integrity_check` ok and the operator
   row present; a dry run that changes no file; a stopped application refused;
   and `deploy.sh preflight` passing against the real production manifest and
   env example, then failing when one required variable is removed.

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
6. [x] Port the store to SQLite and squash thirteen migrations into one baseline.
   There is no data to migrate — a fork starts empty — so the inherited
   migration history buys nothing and carries vibecoin columns forward.

   The data model itself did not change: `projects`, `deploys`, `deploy_sagas`,
   `custom_domains`, `api_keys` and `users` already held everything a project
   and its sites need. Only the engine underneath them moved.

   The driver is `modernc.org/sqlite`, pure Go. The cgo one is faster and
   unusable: every image builds with `CGO_ENABLED=0`, and the static binary is
   most of what keeps the runtime image small.

   Three translations were the whole difficulty, and each is a silent failure
   rather than an error:

   - **Timestamps are TEXT, so every comparison is string ordering.** It is
     correct only because the format is fixed-width RFC 3339 in UTC. SQLite's
     own `datetime('now')` returns `YYYY-MM-DD HH:MM:SS` — the space where the
     `T` belongs sorts before every digit, so `last_used_at < datetime(…)`
     would have marked *every* row expired. Column defaults use `strftime`;
     every comparison is a value computed in Go.
   - **Placeholders are positional.** PostgreSQL reused `$1` three times in one
     `UPDATE`; SQLite needs a `?` and an argument per mention. Four statements
     changed shape, and `admin`'s filters gained argument builders so the
     repetition is defined next to the SQL rather than counted at the call site.
   - **`time.Time` does not scan.** `google/uuid` implements `sql.Scanner`, so
     ids were free; timestamps needed `db.Into` and `db.IntoNull`, which write
     into an existing struct field so the models kept their types.

   The admin integration tests are the visible win. They needed a PostgreSQL
   container and an `ADMIN_TEST_DATABASE_URL`, so they ran when someone
   remembered and never in CI. The database is now a file in a temp directory
   and they run like any other test — which is how three scan errors in this
   port were caught.

   Five new tests cover the schema itself: that the baseline applies, that its
   down migration is a real reversal, that foreign keys are actually enforced
   rather than merely declared, that an `updated_at` trigger fires, and that
   the timestamp format is what everything else assumes.

6a. [x] Remove Supabase, and issue identity ourselves.

   `SUPABASE_URL` had exactly one use: fetching JWKS to verify the signature on
   somebody else's JWT. That is a multi-tenant SaaS's identity provider, and
   this platform has one operator. The prefetch was fatal on failure, so an
   external service being unreachable stopped this process starting at all.

   Out: `JWKSCache`, `SupabaseClaims`, `VerifyToken`, the JWK parsers, the
   signup webhook and the `AccountCreator` behind it, and the dead
   `auth/register` and `auth/login` entries in `PublicRoutes` — routes that
   never existed here, because registration went straight from the browser to
   Supabase.

   In: an operator row with an argon2id password hash, login, logout, `me` and
   password change, a session cookie (HttpOnly, SameSite=Lax, Secure derived
   from the request unless `SESSION_COOKIE_SECURE` forces it), and a password
   generated on first start and printed once to the log rather than shipped as
   a default.

   **Sessions are rows, not self-signed JWTs.** The decision, since it was a
   real fork in the road: a locally signed token is fewer moving parts until
   logout has to revoke and a password change has to invalidate every other
   session. Neither is optional. A JWT reaches both by keeping a denylist until
   each token expires, which is the session table with a worse name plus a
   signing key to generate, persist and rotate. The usual argument for the
   stateless token — saving a round trip to a session store — does not apply
   when the store is a file this process already has open. The cost is one
   indexed read per authenticated request, and a sliding expiry bounded to one
   write an hour so it stays off the read path.

   `user_id` stays on every table, as planned. Keeping it turned out to have an
   immediate use rather than a hypothetical one: the log stream had no
   ownership check at all, so any valid credential could subscribe to any
   deploy's logs by id. Unreachable with one account, which is what makes it
   the kind of authorisation that stops holding the moment there are two.

   Two things the work turned up that were not on this list:

   - **The WebSocket had to change more than its credential.** It read a token
     from a query parameter; it reads the cookie now, and a handshake is not
     subject to the same-origin policy, so `CheckOrigin` returning true
     unconditionally became a cross-site hijack. It checks the Origin first,
     before the deploy is looked up, so a refused origin cannot learn whether
     an id exists.
   - **argon2id's memory parameter is a real cost here, and the obvious value
     was the wrong one.** At OWASP's most-quoted configuration (m=19456, t=2)
     three sequential logins took the process from 8.2 MiB resident to 65.3,
     and it stayed there — Go sizes its heap to about twice the live set and
     does not give it back. OWASP publishes five configurations it treats as
     equivalent, trading memory against iterations; the cheapest, m=7168/t=5,
     is the same defence for a third of the memory. Measured after the change:
     7.4 MiB idle, 28.8 after three logins, 28.8 after thirteen, 36 ms per
     login. The ~21 MiB that remains is heap sizing rather than argon2, and it
     is what item 8 exists to bound.

   Ordered after item 6 on purpose: this adds a table, and item 6 was already
   rewriting the migration history into one baseline. Doing it in this order
   put the operator and session tables in that baseline instead of a migration
   on top of it.

   Verified live from an empty volume, which is the acceptance test for the
   item: the stack starts with no `SUPABASE_URL` set anywhere.

7. [x] Replace Redis with the in-process queue, pub/sub, and credential store.

   Eight jobs, not five: two queues, the log pub/sub, the bounded log history
   the HTTP endpoint read back, the build-event channel, uploaded archives, git
   credentials, and two rate limiters. Every one of them was a broker between
   goroutines in one process.

   Durability came out cheaper than planned. The task decisions call for "a
   channel plus a durable table", and the durable table already existed:
   `deploy_sagas` plus the resume sweeper. What was added instead is one query
   at startup that rewinds sagas stranded mid-build, so a restart costs a fresh
   build rather than the whole 15-minute build timeout followed by
   compensation.

   Four things this found that were not on the list:

   - **The build queue's redelivery was already broken.** An un-acked job
     would have been replayed against an uploaded archive and a git credential
     that the pipeline deletes the moment it consumes them, so for two of the
     three source types the replay could only fail.
   - **The per-user in-flight count leaked.** It was a Redis sorted set with no
     expiry on its members, so a build killed mid-flight shrank that user's
     concurrency limit permanently.
   - **Git tokens were being written to disk for no reason.** Redis ran with
     appendonly persistence, so every private-repository token was appended to
     a file in plaintext — and nothing ever read it back, because the token
     lives for minutes and the builder deletes it after the clone.
   - **A race in the first draft of the log bus.** Delivery copied the
     subscriber channels out from under the lock and sent afterwards, so an
     unsubscribe landing in that window closed a channel a publisher was about
     to send on. That is a panic, and the way to reach it is closing a browser
     tab during a build. Found by the race detector on the first concurrent
     run.

   Two decisions were taken rather than assumed, because the task's own
   decisions did not cover them:

   - **Log history is memory plus a tail on disk.** Redis persisted with
     appendonly and memory does not. For a build someone watched succeed that
     is nothing; for one that failed it is the whole reason to open the deploy
     again, so the last 200 lines go to `deploys.log_tail` at the moment the
     status becomes `failed`.
   - **Rate limiting survives on the login endpoint only.** The decision that
     it "stops being a concept" was written before item 6a added a password.
     The sliding window that shaped traffic for a multi-tenant API is gone;
     ten failures per address per five minutes is what replaced it, on the one
     endpoint that accepts a guess at the credential which opens a panel that
     runs containers as root.
8. [ ] Set `GOMEMLIMIT` and a container memory limit that agree with each other.
   Neither `GOMEMLIMIT` nor `GOGC` is set anywhere in the inherited tree, while
   the production Compose file does set container memory limits — so Go never
   learns about the ceiling it is running under and grows its heap until the
   kernel intervenes.

   Item 6a gave this a measured case rather than a principle. A login allocates
   7 MiB for the password hash and the process goes from 7.4 MiB resident to
   28.8 and stays there, because the heap is sized against the peak live set and
   nothing scavenges it back. It plateaus, so it is not a leak — but a quarter
   of the platform's idle footprint is now heap the runtime is holding on to
   for an operation that happens a few times a week.
9. [ ] Rewrite `CLAUDE.md` and the architecture docs, which currently describe
   seven services and a cloud runtime that no longer exist.

   **Done last, after item 10**, because its whole job is to describe what is
   there. Items 7 and 10 both change what that is — Redis leaves the manifest,
   and the panel moves inside the binary along with a Node stage in the image
   build. Writing this before them means writing the same two sections twice.

   The cost is that `CLAUDE.md` stays wrong for one more item, while being the
   file the work is done against. It carries a banner saying so, so the tax is
   at least visible; if it starts costing more than the double write would,
   move this ahead of item 10 and accept rewriting the panel section.

10. [ ] The panel, embedded in the binary.

    There is no frontend in this repository. It stayed in `justaba/snaphost-ui`,
    which was not forked: React 19 and Vite, 180 files, about 9,800 lines, 21
    pages — including the whole operator console, projects, domains and API
    keys. Rewriting that is more expensive than taking it and cutting, the same
    argument the backend was forked on.

    **Served by `go:embed`, not by a second container and not from disk.** A
    static-file container would undo the point of items 4 and 5, and a
    disk artifact brings its own deployment step and its own SHA — upstream's
    docs say to "record backend and frontend release SHAs independently", which
    is precisely the skew this avoids. One binary contains the API and the panel
    it serves, and they cannot disagree about their own version.

    The cost is a Node stage in the image build. It lands in the builder stage
    only; the runtime image gains the built assets and nothing else.

    Dropped: `signup`, `recover`, `reset-password`, `auth-callback` — Supabase
    flows that item 6a removes the backend for; `legal-document` and
    `legal-index`, which served a Russian consumer SaaS's offer and privacy
    policy; `admin-transactions`, which reads a ledger that no longer exists.
    `login` stays and is rewired to the operator session.

    Kept: projects, project detail, domains, API keys, settings, and the six
    admin pages. Supabase appears in four files, so the auth swap is small.

    This is a first pass, not the last word on the panel. Tasks 2 and 3 add
    environment variables, volumes and managed services, and each of those
    brings its own screens.

## Order of the remaining items

**8 → 10 → 9**, then [Task 7](../planned/0007-install-and-upgrade.md).

One of those placements is a dependency rather than preference: **9 last**,
because its job is to describe what is there and item 10 changes what that is.
See item 9.

Item 7 came before 8 for the same kind of reason, and it landed on 2026-08-30:
it moved a 50 MB upload out of RAM and brought two in-process queues with heaps
of their own, so a `GOMEMLIMIT` picked before it would have been re-tuned
straight afterwards.

Item 10 moved ahead of 9, which reverses what this document said until
2026-08-29. The reason it gave — "10 sits last because it logs into 6a" — was
about a dependency, and 6a is done, so nothing holds 10 at the end any more.

**Task 7 after all of them.** It rewrites `docker-compose.prod.yml`,
`deploy.sh` and `deploy_test.sh`, and items 7, 8 and 10 each touch those same
three files; doing it first means rewriting them three more times. Its own
work of shrinking the environment surface wants to shrink a settled set once,
and items 7 and 8 are still removing and adding variables. There is no urgency
pulling the other way: no one is installing this yet, and the two defects that
made the deployment path actually broken were fixed on 2026-08-29.

The honest argument against this order is that it leaves the product
uninstallable by anyone else for longer. It is weaker than it looks — Task 7
finishing before item 10 would deliver an install procedure for a platform with
no interface, which is not a thing to hand anyone.

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
