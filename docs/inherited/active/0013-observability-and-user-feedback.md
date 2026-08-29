# Task 13 — Observability split: admin logs vs. user-facing feedback

**Status:** 13a implemented but **not working in production** — Yandex refuses
the log group with `PermissionDenied` and the cause is unresolved; collection is
switched off there. 13b planned
**Created:** 2026-07-08
**Updated:** 2026-08-04

## 13a implementation (2026-08-04)

Runtime logs are now collected. Every Yandex Serverless Container revision is
deployed with `log_options`
([logging.go](../../../snaphost-backend/runner-svc/internal/backend/yandex/logging.go)),
so a user application's `stdout`/`stderr` lands in a dedicated Cloud Logging
group instead of being discarded.

- **Terraform** ([logging.tf](../../../terraform/yandex/logging.tf)) creates a
  dedicated `snaphost-runtime` group with its own retention (default 168h),
  rather than using the folder's default group — retention is set per group,
  and untrusted user output should not interleave with the control plane's own
  logging. Output `runtime_log_group_id` feeds `YANDEX_LOG_GROUP_ID`.
- **IAM**: the runner service account gained `logging.writer`. Revisions write
  as the service account attached to them, so without it `log_options` are
  accepted and silently produce nothing — the failure mode this change is
  supposed to remove.
- **Destination precedence**: an explicit group when configured; otherwise the
  folder's default group, so output is never simply dropped; `Disabled` only
  when explicitly turned off or when no destination can be named at all.
  An unrecognised `YANDEX_RUNTIME_LOG_MIN_LEVEL` keeps the provider default
  rather than guessing, because a wrong minimum silently drops exactly what
  someone is trying to read.

Item 2 (no infrastructure detail in user-visible streams) is what closed the
loop the other way. `Service.StreamContainerLogs` publishes whatever the
backend's `StreamLogs` yields straight to the deploy owner's WebSocket, and the
Yandex implementation was a stub emitting a placeholder line into that stream.
It now returns `ErrRuntimeLogsAreOperatorOnly` instead, so raw runtime output
has no path to the user at all — which is the decision in the table below,
enforced in code rather than by the stub happening to be unfinished.

**Applied to production 2026-08-04.** The plan was exactly `2 to add, 0 to
change, 0 to destroy` — the log group and the `logging.writer` binding — with
every existing resource (API Gateway, wildcard certificate, DNS zones and
records, registry, Lockbox, router container, both service-account keys)
refreshing with no change proposed. A follow-up plan reports no drift. The
group is `e232536al99cst2cpqph`, and `YANDEX_LOG_GROUP_ID` is set in the
production env file.

### It does not work yet, and the attempt caused an outage

The acceptance check was run on production on 2026-08-04 with a deliberately
failing container. Every attempt died at revision deploy:

```
PermissionDenied: Not enough permissions to use log group e232536al99cst2cpqph
```

Because the first implementation always attached log options, **this took every
new deploy down** — a logging misconfiguration became a total outage for the
deploy path. Production now runs with `YANDEX_RUNTIME_LOGS_DISABLED=true` and
containers write to the folder's default group, which is where they went before
this task started.

What was tried and ruled out:

- folder-level `logging.writer` on the runner service account — granted by the
  original apply, still refused;
- folder-level `logging.viewer` on top of it — no change after 15 minutes, so
  not IAM propagation either. Reverted, since an unjustified grant that does
  not help is worse than none;
- the runner SA already holds folder-level `logging.reader`.

So the missing grant is not a folder-level logging role. The most likely
remaining explanation is a binding on the **log group resource itself**, which
this provider version cannot express — there is no `yandex_logging_group_iam_*`
resource in 0.204.0. Next step is to grant the runner service account access on
the group directly through the console or the IAM API, retest, and if that
works, decide how to keep it in Terraform.

### What was changed so this cannot happen again

Log collection is now best-effort and can never be the reason a deploy fails:

- an unconfigured `YANDEX_LOG_GROUP_ID` sends **no** `log_options` at all rather
  than naming the folder. Yandex then applies its own default and writes to the
  folder's default group, so the outcome is identical without a permission
  check that can fail;
- when `DeployRevision` is rejected specifically over the log group
  (`PermissionDenied`, `NotFound`, or `InvalidArgument` mentioning a log group),
  the revision is redeployed once without log options, with a `warn` line in the
  deploy log so the loss is visible rather than silent;
- both behaviours are covered by tests, including the exact production error
  string.

The judgement behind it: collecting logs is worth less than being able to
deploy at all.

### The evidence left in the production database

The check ran as a dedicated service user rather than a real account, so it did
not touch anyone's deploy history or balance:

| Field | Value |
| --- | --- |
| user id | `c064d8a4-d823-48a8-b94b-a3623e696061` |
| email | `logprobe-c064d8a4-…@internal.invalid` |
| deploys | 5, all `failed` |
| wallet | 100 coins, 0 reserved — every reservation refunded |

It is kept deliberately as the record of this exercise. The deploy rows carry
the two distinct failure reasons worth recognising later: the log-group
`PermissionDenied`, and the Task 15b probe message about `PORT`. No runtime
survived — the Yandex container count for these deploys is zero.

The test project was a two-file archive whose `Dockerfile` writes a marker to
stdout and stderr and exits `1`, chosen so that it builds and pushes cleanly
and only fails at runtime, which is exactly the case runtime logs exist for.

### What the exercise did prove

- **Task 15b works on production.** The liveness probe caught a container that
  never answered and failed the deploy with the user-facing reason "make your
  server listen on the PORT environment variable" — the acceptance criterion
  Task 15 still had open.
- **Saga compensation works.** Every failed attempt refunded its reservation.
- The archive upload path, project creation, and build pipeline all worked
  end to end against production.

**Docker backend unchanged, and it is worth knowing why.** There,
`StreamLogs` really does forward container `stdout`/`stderr` to the deploy
owner, which contradicts the decision below. It is the local development
backend only — production is Yandex — so the exposure is a dev-loop
convenience rather than a live leak. Aligning it belongs with 13b, which is
where what users see gets decided.

## Problem

Today Redis pub/sub carries build and lifecycle events, and user-billing streams
them to the deploy owner over WebSocket. Two gaps:

- A user-application container's own `stdout`/`stderr` is not collected anywhere
  once it is running on Yandex Serverless Containers, so an operator cannot debug
  a crashed user app.
- When a deploy fails, the user sees a raw status/log stream rather than an
  actionable explanation of what to change in their repository.

## Evidence (2026-07-19 incident)

The first real user-journey test hit both gaps at once. A user deploy
(`d32c6531…` on staging) reached `running` but its URL returned Yandex
`UserCodeError: exit status 1`. Diagnosis required manually pulling the image
onto the staging VDS and running it by hand, because:

- **no runtime logs exist anywhere** — the staging folder has no Cloud Logging
  log group at all; runner-svc creates serverless containers without log
  options, so the container's stderr is discarded (13a);
- the user saw a dead URL with a raw provider error and no explanation that
  their Dockerfile ignored the `$PORT` contract (13b; the validation/liveness
  half of that incident is [Task 15](0015-runtime-port-contract.md)).

13a minimum now has a concrete shape: create/point a log group per environment
and pass `log_options` when creating container revisions, so `UserCodeError`
becomes a queryable log line instead of an SSH archaeology session.

## Decision

Logs have two audiences, treated differently (owner decision, 2026-07-08):

| Audience | Sees |
| --- | --- |
| **User (deploy owner)** | status, **build logs** (clone / Dockerfile / BuildKit / Trivy — these are fixable in their repo), and an **AI-generated recommendation** of what to change. No raw runtime logs. |
| **Operator (admin)** | everything: infrastructure, provider, and user-app `stdout`/`stderr`, collected centrally. |

Rationale: build failures are almost always the user's to fix, so build output
must stay visible to them. Runtime application logs are noisy and can leak
infrastructure detail, so users get a distilled status + recommendation instead,
while operators get the full stream for debugging. This aligns with the existing
security rule that provider errors are redacted before entering user-visible
logs ([security.md](../../architecture/security.md)).

## Work plan

### 13a — Central admin log collection (runtime)

1. [x] Collect user-container `stdout`/`stderr` on the Yandex runtime into a
   central store the operator can query. Prefer Yandex Cloud Logging behind the
   runner/provider boundary (this supersedes the backlog "Yandex Cloud Logging"
   line). Keep it out of the user-facing WebSocket. Implemented 2026-08-04;
   not yet verified by reading a real crashed deploy's output.
2. [x] Ensure infrastructure/provider errors remain redacted from any
   user-visible stream (verify the existing redaction path covers new sources).
   The Yandex backend's `StreamLogs` now refuses instead of publishing, so
   runtime output has no path to the deploy owner. The Docker dev backend still
   forwards it — noted above, and in scope for 13b.

### 13b — User-facing recommendations

3. [ ] Keep build logs streaming to the deploy owner (already implemented) —
   this is the primary "why did it fail" signal.
4. [ ] On a failed build/deploy, generate a short, human-readable recommendation
   ("add a Dockerfile that listens on `$PORT`", "pin the base image", "the build
   ran out of memory") from the captured error. Reuse `ai-orchestrator`, which is
   already wired for LLM calls, behind an internal endpoint; cache by error
   signature to avoid re-hitting the model.
5. [ ] Surface `status + recommendation` in the deploy API/UI as the default
   failure view; raw runtime logs are not exposed to the user.

## Acceptance criteria

- An operator can retrieve a crashed user container's runtime logs from a central
  store; users cannot.
- A failed deploy shows the user a concise, actionable recommendation, not raw
  runtime output.
- Build logs remain visible to the deploy owner.
- No infrastructure/provider detail leaks into any user-visible surface.

## Notes

Scope is deliberately split so 13a (operator observability) and 13b (user
feedback) can ship independently. 13a is infrastructure/provider work behind the
runner boundary; 13b is a product feature on top of the existing saga error path
and `ai-orchestrator`.
