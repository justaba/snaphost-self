# Task 15 — Runtime port contract: validate user Dockerfiles and probe liveness

**Status:** Implemented 2026-07-30 — 15a and 15b landed; item 6 waits on
Task 13b, and staging proof is still outstanding.
**Created:** 2026-07-20
**Updated:** 2026-07-30

## Implementation status (2026-07-30)

- **15a** — `detect.FixedPortRisk`
  ([port.go](../../../snaphost-backend/builder-svc/internal/detect/port.go))
  flags a user-shipped Dockerfile that exposes a numeric port and never
  mentions `PORT`; the pipeline publishes a `warn` line naming the port, the
  fix, and when to ignore it. Warning only, per item 2: a Dockerfile cannot see
  that the app reads `PORT` at runtime, and any mention of `PORT` outside
  `EXPOSE` silences the check. The contract itself is now stated in one place
  ([yandex-runtime.md](../../architecture/yandex-runtime.md)).
- **15b** — `backend.Prober` is an optional interface, so each backend keeps
  its own way of reaching the runtime it started (ADR 0004): the Docker backend
  asks the container on its network address, the Yandex backend invokes the
  container's signed URL and treats 502/503 as "still cold, retry". Backends
  that do not implement it are not probed, which is the pre-15 behavior.
  `runner-svc` probes before reporting `running`; on failure it tears the
  runtime down, marks the deploy `failed` with a reason naming `PORT`, and
  answers `422 probe_failed`. The saga now treats a 4xx from runner-svc (except
  429) as terminal, so the reservation is refunded instead of the job being
  requeued — previously every runner refusal looked transient.
  Controlled by `RUNTIME_PROBE_ENABLED` and `RUNTIME_PROBE_TIMEOUT_SEC`.

Not done:

- **Item 6** — feeding the probe reason into the Task 13b recommendation flow.
  That path does not exist yet; the reason is already user-facing in the deploy
  logs and in `deploys.failure_reason`.
- **Acceptance proof** — the third criterion (a `$PORT`-honoring template still
  passes the probe through a cold start) can only be shown on the Yandex path,
  which needs a staging deploy. Locally the Docker probe was exercised by unit
  tests, not by a real container.

**15b proven on production 2026-08-04.** While testing Task 13a, a container
that deliberately exits on startup was deployed through the real pipeline
(archive upload → build → Trivy → push → Yandex revision). The probe caught it
and the deploy ended `failed` with the user-facing reason "the container
started but nothing answered on the port the runtime injects. Make your server
listen on the PORT environment variable", and the reservation was refunded —
never a billed `running` with a dead URL, which is the second acceptance
criterion. The remaining gap is the third criterion: a working `$PORT` template
passing the probe through a cold start.

## Problem

Yandex Serverless Containers invoke the deployed image on the port passed in
the `PORT` environment variable (8080). Our AI-generated Dockerfiles honor
`$PORT` (fixed in Task 11.17), but user-shipped Dockerfiles are built and
deployed with no check of this contract.

Observed on the first real user-journey test (2026-07-19, deploy
`d32c6531-8f50-4102-8b41-f1a600d75ac8` on staging): an editor AI generated a
Vite project with its own Dockerfile (`vite build` → `nginx:alpine`,
`EXPOSE 80`, nginx hard-bound to port 80). The pipeline built, scanned, and
pushed it; the saga reached `running` and **committed the coins** — but every
request to the public URL returned Yandex's
`{"errorType":"UserCodeError","errorMessage":"exit status 1"}` because nothing
listened on `$PORT`. Reproduced by running the image with `PORT=8080`: nginx
serves on 80, connection refused on 8080.

Two product failures compound here:

1. an incompatible Dockerfile sails through the whole pipeline with no warning;
2. the deploy is reported (and billed) as `running` without anyone having
   checked that the public URL actually answers.

## Work plan

### 15a — Dockerfile port-contract validation (builder-svc)

1. [x] In the existing Dockerfile validation stage, for user-shipped
   Dockerfiles: detect a fixed-port serve setup (e.g. `EXPOSE` of a port with
   no `$PORT`/`${PORT}` reference anywhere in the Dockerfile) and publish a
   prominent build-log warning: "the runtime injects PORT and invokes your
   container on it; a server bound to a fixed port will deploy but never
   receive traffic".
2. [x] Owner call on strictness: warning-only first (never break a repo that
   also reads PORT at runtime in app code — a Dockerfile-level check cannot see
   that), revisit fail-fast after observing false-positive rate.
3. [x] Extend the AI Dockerfile generation prompt/templates note in docs so the
   `$PORT` contract is stated in one place
   ([yandex-runtime.md](../../architecture/yandex-runtime.md)).

### 15b — Liveness probe before `running` (runner-svc / saga)

4. [x] After runner-svc reports the container started, probe the public URL
   (or the invocation endpoint) with a bounded retry window (serverless cold
   start can 502/503 for ~10s) before the saga transitions the deploy to
   `running` and commits the reservation.
5. [x] On probe failure: mark the deploy `failed` with a user-facing reason
   ("the container started but nothing answered on $PORT"), tear down the
   runtime, refund via the existing compensation path.
6. [ ] Feed the probe-failure reason into the Task 13b recommendation flow so
   the user gets "make your server listen on the PORT environment variable"
   instead of a raw provider error.

## Acceptance criteria

- A user Dockerfile that hard-binds a port produces a visible warning in the
  build logs the deploy owner streams.
- A deploy whose container never answers on `$PORT` ends `failed` with a clear
  reason and a refund — never a billed `running` with a dead URL.
- The Task 11.17-style static template deploys (which honor `$PORT`) still pass
  the probe unchanged, including through a cold start.

## Notes

Found by, and blocking, the Task 14 user-journey test. Related: Task 13a — the
same incident showed runtime logs are collected nowhere (no log group even
exists in the staging folder), which made this diagnosis require manually
running the image; 13a would have made it a log query.
