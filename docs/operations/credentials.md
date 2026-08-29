# Credentials

Status: Current — layout, source of truth, and the preflight guard implemented; Lockbox sourcing not
Type: Operations
Updated: 2026-08-04

Staging and production act in different Yandex folders, with different service
accounts, against different registries. The key files are structurally
identical JSON, so nothing about a file tells you which environment it belongs
to — and crossing them fails **late**: the deployment succeeds, and the first
user build dies at registry push with `403 Forbidden`. That happened during the
2026-07-08 staging rebuild.

Three things prevent a repeat: a layout that makes the environment explicit, a
documented source of truth, and a preflight check that refuses a mismatch.

## Service accounts

| Environment | builder | runner | Terraform bootstrap |
| --- | --- | --- | --- |
| production | `aje09arolbme1vf12mip` | `ajepvolmfratktos500f` | `ajeu0cij7l1u3o5inehn` |
| staging | `ajefqrjr8kdac0s9i5nv` | `ajemp5e9undlhrgio9pk` | `ajescv6ubj69vao4pvh7` |

The bootstrap accounts are not interchangeable with the other two and are not
covered by the preflight guard below, because they never reach a host: they
authenticate the Terraform provider from a workstation at apply time.

Verify against Terraform outputs before trusting this table — service accounts
can be recreated:

```bash
cd terraform/yandex
terraform output builder_sa_id
terraform output runner_sa_id
```

## The preflight guard

`deploy.sh preflight` reads `service_account_id` out of each mounted key and
compares it with the environment's own configuration:

| Key | Must match |
| --- | --- |
| `BUILDER_KEY_PATH` | `YANDEX_BUILDER_SA_ID` |
| `RUNNER_KEY_PATH` | `YANDEX_RUNNER_SA_ID` |

A mismatch stops the deployment before Docker login, any image pull, or any
Yandex call:

```
ERROR: builder key belongs to service account aje09arolbme1vf12mip, but this
environment expects ajefqrjr8kdac0s9i5nv: refusing to deploy another
environment's credentials
```

`YANDEX_RUNNER_SA_ID` is not a new setting — it is the identity `runner-svc`
already creates containers as, so a key that disagrees with it was always
broken. `YANDEX_BUILDER_SA_ID` is new and **required**: an absent expectation
would silently disable the check, so preflight fails when it is missing rather
than skipping the comparison.

Covered by seven scenarios in
[deploy_test.sh](../../infra/tests/deploy_test.sh): wrong builder key, wrong
runner key, no Docker or Yandex call made on refusal, a key with no
`service_account_id`, a missing expectation, matching keys, and realistically
formatted key JSON.

## Where each environment's keys come from

The single source of truth is Terraform, not a copy on someone's laptop:

| Output | Resolves to | Used as |
| --- | --- | --- |
| `builder_key_path` | `terraform/yandex/${key_output_dir}/builder.json` | `BUILDER_KEY_PATH` |
| `runner_key_path` | `terraform/yandex/${key_output_dir}/runner.json` | `RUNNER_KEY_PATH` |

`key_output_dir` is `.keys/` for production and `.keys/staging/` for staging.

On an S3 backend these files exist only in the working copy at apply time. If
they are absent, **regenerate** through `yandex_iam_service_account_key` or the
IAM API and re-import — never reuse the other environment's file. A key you
cannot trace to an environment is not a fallback.

## Local layout

`secrets/` on the workstation is fully gitignored and split by environment:

```
secrets/production/   bootstrap-key.json, builder-key.json, runner-key.json,
                      ghcr-token, terraform-backend-access-key.json, supabase/
secrets/staging/      bootstrap-key.json, terraform-backend-access-key.json,
                      host.env, host/ (SSH identity, AppArmor, Caddy bootstrap)
```

**No key file sits at the root.** `secrets/README.md` carries the per-file
inventory.

Note the privilege gradient inside `production/`. The **bootstrap key** (SA
`ajeu0cij7l1u3o5inehn`) is the Terraform provider credential: it can create and
destroy infrastructure, is used from a workstation at apply time, and must never
reach a host or a container. The builder and runner keys are narrowly scoped by
comparison — pushing images and running containers — and are the only two that
belong on the VDS.

`terraform.tfvars` references the bootstrap key by relative path
(`bootstrap_key_file`), so **relocating it silently breaks `terraform plan`**
until that path is updated. That is worth knowing before tidying this
directory: it is the one file here whose location is load-bearing.

## On the hosts

Production keeps its keys in `/opt/snaphost/secrets/`, mode `0600`, owned by
`deployer` (`1000:1000`). The builder key is mounted only into `builder-worker`;
the runner key only into `runner-api` and `runner-watchdog`. Terraform
bootstrap credentials are never mounted into a runtime service.

Verify what a host is actually holding:

```bash
for f in /opt/snaphost/secrets/*.json; do
  printf '%s %s\n' "$(basename "$f")" \
    "$(grep -o '"service_account_id"[^,]*' "$f")"
done
```

## Not done

Sourcing keys from Lockbox instead of operator-copied files. Lockbox already
holds the router webhook secret, so a fresh host could fetch its keys from one
audited store rather than having them carried by hand. The preflight guard
makes the manual path safe to get wrong; it does not remove the manual path.

Related: [Task 12](../tasks/active/0012-credential-separation.md),
[production deployment](production-deployment.md),
[staging provisioning](staging-provisioning.md).
