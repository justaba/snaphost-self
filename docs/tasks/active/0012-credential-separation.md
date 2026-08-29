# Task 12 — Separate and source staging/production service-account keys

**Status:** Implemented 2026-08-04 except Lockbox sourcing (item 4)
**Created:** 2026-07-08
**Updated:** 2026-08-04

## Implementation status (2026-08-04)

Items 1, 2, 3, and 5 are done; item 4 (Lockbox) is deliberately not, and is
recorded as such below. The operations reference is
[credentials.md](../../operations/credentials.md).

The guard is the part that matters: `deploy.sh preflight` reads
`service_account_id` out of each mounted key and compares it against
`YANDEX_BUILDER_SA_ID` / `YANDEX_RUNNER_SA_ID` from that environment's env
file, refusing before Docker login, image pull, or any Yandex call. Reusing
`YANDEX_RUNNER_SA_ID` was deliberate — it is already the identity `runner-svc`
creates containers as, so a key disagreeing with it was always broken, and the
check costs no new configuration on the runner side. `YANDEX_BUILDER_SA_ID` is
new and required, because an absent expectation would silently disable the
comparison.

Verified against production on 2026-08-04: the deployed builder and runner keys
match `aje09arolbme1vf12mip` and `ajepvolmfratktos500f`, and preflight passes
with the guard active.

One finding the audit produced, and the correction that followed: the flat pile
contained `authorized_key.json` (SA `ajeu0cij7l1u3o5inehn`), matching no service
account in this document or in either environment's Terraform outputs, so it was
first filed as unidentified. It is in fact the **production Terraform bootstrap
credential** — `terraform.tfvars` referenced it by relative path as
`bootstrap_key_file`, and moving it broke `terraform plan` until the path was
updated. It now lives at `secrets/production/bootstrap-key.json`.

That is the sharpest argument for this task, not against it: the most privileged
credential in the repository was sitting at the root under a name that said
nothing about what it was or which environment it belonged to, next to keys that
belong on a host — which this one must never reach. The table below lists
builder and runner accounts only, which is why it matched nothing; a bootstrap
row has been added to
[credentials.md](../../operations/credentials.md).

Also moved out of the flat pile: a Terraform state snapshot
(`tfstate-preremote-*.json`) that embeds generated private keys and had been
sitting beside the live credentials as if it were a backup artifact.

## Problem

Yandex service-account authorized keys give a backend service permission to act
in one environment: `builder` pushes images to that environment's registry,
`runner` creates and invokes that environment's Serverless Containers. Staging
and production use different service accounts with different registries, so a
staging host must receive staging keys and a production host must receive
production keys. Crossing them fails closed — a production builder key on a
staging host cannot push to the staging registry and returns `403 Forbidden`.

The current local `secrets/` directory is a flat pile that mixes environments:
`builder-key.json` and `runner-key.json` are the **production** keys (service
accounts `aje09arolbme1vf12mip` and `ajepvolmfratktos500f`) with no environment
in the filename, sitting next to `staging-*` files. During the 2026-07-08
staging VDS rebuild these production keys were mistakenly deployed to the staging
host, and every user build failed at registry push with `403` until fresh
staging keys were minted and installed. The real staging keys are not stored
locally in any obvious place — they live only inside the S3 Terraform state (or
must be regenerated), so there was no documented "source of truth" to copy from.

## Goal

Make it structurally hard to deploy the wrong environment's keys, and give a
single documented procedure for obtaining the correct keys for each environment.

## Work plan

1. [x] Split local key storage by environment: `secrets/production/` and
   `secrets/staging/`, each containing its own `builder-key.json`,
   `runner-key.json`, and supporting files. No key file at the `secrets/` root.
   `secrets/` stays fully gitignored. A third directory, `unidentified/`, holds
   keys that cannot yet be attributed — the alternative was leaving one in the
   pile it was supposed to be cleaned out of.
2. [x] Document the single source of truth for each environment's keys: the
   Terraform outputs `builder_key_path` / `runner_key_path` (which resolve to
   `terraform/yandex/${key_output_dir}/{builder,runner}.json`, i.e. `.keys/` for
   production and `.keys/staging/` for staging), plus `builder_sa_id` /
   `runner_sa_id` for verification. Note that on an S3 backend these files exist
   only in the working copy at apply time; if absent, regenerate via
   `yandex_iam_service_account_key` or the IAM API and re-import, never reuse the
   other environment's file.
3. [x] Add a preflight guard in the bring-up path (bootstrap and/or
   `deploy.sh preflight`) that reads `service_account_id` from each mounted key
   and compares it to the expected `builder_sa_id` / `runner_sa_id` for the
   target environment, refusing to proceed on mismatch. This turns the whole
   class of "wrong-environment key" bugs into an early, explicit failure.
   Implemented in `check_key_identity`, running before Docker login, image
   pull, and any Yandex call, with seven test scenarios.
4. [ ] Optionally source keys from Lockbox (already used for the router webhook
   secret) instead of local files, so a fresh host fetches keys from one audited
   store rather than an operator copying JSON by hand. **Not done, and the
   reason to keep it open:** the guard makes the manual path safe to get wrong,
   but it does not remove the manual path. A fresh host still depends on an
   operator carrying two JSON files to the right machine.
5. [x] Update `docs/operations/staging-provisioning.md` and
   `docs/operations/production-deployment.md` to reference the split layout, the
   Terraform-output source, and the SA-id preflight check — all three now point
   at [credentials.md](../../operations/credentials.md), which holds the detail
   in one place rather than duplicated across both.

## Acceptance criteria

- Production and staging keys live in separate, clearly named locations; no key
  sits at the `secrets/` root.
- A documented, repeatable procedure states exactly where each environment's
  builder/runner keys come from.
- Deploying a key whose `service_account_id` does not match the target
  environment stops the bring-up before any Yandex call.
- The procedure and guard are documented in the operations docs.

## Notes

Expected service accounts (2026-07-08):

| Environment | builder SA | runner SA |
| --- | --- | --- |
| production | `aje09arolbme1vf12mip` | `ajepvolmfratktos500f` |
| staging (`snaphost-staging-*`) | `ajefqrjr8kdac0s9i5nv` | `ajemp5e9undlhrgio9pk` |

Staging folder is `b1gdhkvdc0cgn681caft`; staging registry is
`cr.yandex/crppks7vsukk84sv064s/snaphost`. Verify these against Terraform outputs
before trusting them — service accounts can be recreated.
