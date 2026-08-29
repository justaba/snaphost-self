# Staging infrastructure provisioning contract

Status: Staging infrastructure provisioned; first deployment under verification
Type: Operations
Updated: 2026-07-08

This document defines how to plan an isolated Yandex staging environment from
the existing `terraform/yandex` root without changing Terraform resource
addresses or touching production state.

## Current-root audit and risks

The root previously had no remote backend declaration, so local state could be
silently selected by the operator's working directory. The same resource
addresses describe DNS, certificate, registry/repository cleanup, runner and
builder service accounts/keys/IAM bindings, API Gateway, router container, and
Lockbox access. Using one state for two environments would therefore propose
replacement or mutation of the resources already recorded in that state.

Several cloud names were hardcoded (`snaphost-router`, `snaphost-wildcard`,
`snaphost-router-svc`, and `snaphost-cleanup`). They are now input variables
whose defaults exactly preserve existing production names. Existing service
account and registry names were already variables. DNS zone identity derives
from `domain_name`; staging must use a different domain. Staging also requires
a different `folder_id`.

Terraform creates authorized service-account keys and writes them through
`local_sensitive_file`. Their private material is also present in Terraform
state, so both remote state and generated `.keys/<environment>/` files are
secrets. Staging sets `key_output_dir = ".keys/staging"`; these files are ignored
and must be copied securely to only the staging VDS. Bootstrap credentials are
Terraform-only and never enter runtime or GitHub Actions.

No resource was moved into a module and no resource address changed. There are
no `moved` blocks or implicit state migration in this change.

## State isolation model

Use the S3-compatible Yandex Object Storage backend with a dedicated staging
bucket and a staging-only access-key pair. The service account behind those
credentials must have no permission on the production state bucket. A unique
key inside the staging bucket may be:

```text
snaphost/staging/terraform.tfstate
```

Production requires its own bucket and production-only access-key pair; its
service account must have no permission on the staging bucket. Different keys
in one bucket are not an acceptable environment boundary. Never reuse a
`.terraform/` directory while switching environments: use separate clean
working copies for staging and production.

Both state buckets must have object versioning and server-side encryption
enabled and public access disabled at both bucket and ACL levels. For Yandex,
the fixed `https://storage.yandexcloud.net` endpoint in versioned Terraform
code is the accepted TLS control; HTTP endpoints and TLS-verification bypasses
are forbidden. The incompatible AWS `aws:SecureTransport` policy must not be
applied. Use separate environment-specific encryption keys where KMS keys are
available. Enable access/audit logging, grant only the operations
required by the backend, and serialize operator access because this backend has
no repository-managed state lock. Terraform state contains private key material
and must be handled as a secret.

Adding the backend declaration does not migrate existing production local
state automatically. Before any future production migration:

1. freeze Terraform changes and make an offline protected copy of the local
   state;
2. prepare a production backend config for the dedicated production bucket and
   load production-only credentials that cannot access the staging bucket;
3. run `terraform init -migrate-state -backend-config=<production-config>` in
   the production working copy;
4. verify the credential identity, the intended production bucket, and denied
   access to the staging bucket before each `terraform state list` and plan;
5. compare `terraform state list` before and after;
6. run and review a production plan expecting no address changes or resource
   replacement.

Do not use `-migrate-state` for the new staging key. Its initial state must be
empty. On a new S3 backend Terraform reports `No state file was found` rather
than returning an empty `terraform state list`. Verify the exact state object
is absent with the S3 `head-object` API before the first plan. If the object
exists or any resource address is returned, stop and correct the backend.

## Safe staging plan sequence

Copy the examples to ignored files, then replace every placeholder. Keep
backend credentials only in the operator process environment. The operator
workstation needs authenticated Yandex Cloud CLI (`yc`), AWS CLI, and `jq` for
the identity and Object Storage access gates below.

```bash
cd terraform/yandex
set -euo pipefail
cp environments/staging.backend.hcl.example environments/staging.backend.hcl
cp environments/staging.tfvars.example environments/staging.tfvars
chmod 600 environments/staging.backend.hcl environments/staging.tfvars

export AWS_ACCESS_KEY_ID='<staging-state-access-key-id>'
export AWS_SECRET_ACCESS_KEY='<staging-state-secret-key>'
export EXPECTED_STAGING_STATE_SERVICE_ACCOUNT_ID='<staging-state-service-account-id>'
export STAGING_STATE_BUCKET='<dedicated-staging-state-bucket>'
export PRODUCTION_STATE_BUCKET='<dedicated-production-state-bucket>'

verify_staging_state_boundary() {
  local access_key_json
  access_key_json="$(
    yc iam access-key get "$AWS_ACCESS_KEY_ID" --format json
  )" || return 1
  if ! jq -e --arg expected "$EXPECTED_STAGING_STATE_SERVICE_ACCOUNT_ID" \
    '.service_account_id == $expected' >/dev/null <<<"$access_key_json"; then
    echo 'backend access key has an unexpected service-account owner; refusing' >&2
    return 1
  fi
  grep -Fqx "bucket = \"$STAGING_STATE_BUCKET\"" \
    environments/staging.backend.hcl || return 1
  aws --endpoint-url https://storage.yandexcloud.net \
    s3api head-bucket --bucket "$STAGING_STATE_BUCKET" || return 1
  if aws --endpoint-url https://storage.yandexcloud.net \
    s3api head-bucket --bucket "$PRODUCTION_STATE_BUCKET" 2>/dev/null; then
    echo 'staging backend credentials can access production state; refusing' >&2
    return 1
  fi
}

# Negative identity check: a deliberately wrong expected owner must be rejected
# before init. This performs no Terraform command and changes no remote state.
expected_owner="$EXPECTED_STAGING_STATE_SERVICE_ACCOUNT_ID"
EXPECTED_STAGING_STATE_SERVICE_ACCOUNT_ID='deliberate-mismatch'
if verify_staging_state_boundary; then
  echo 'identity mismatch was accepted; refusing' >&2
  exit 1
fi
EXPECTED_STAGING_STATE_SERVICE_ACCOUNT_ID="$expected_owner"

verify_staging_state_boundary

terraform init -reconfigure \
  -backend-config=environments/staging.backend.hcl
terraform fmt -check
terraform validate

# Repeat the identity/config/bucket gate immediately before every state read
# and plan; do not rely on an earlier shell-session check. For a brand-new
# backend, require head-object for the configured key to return not found.
verify_staging_state_boundary
state_error=$(mktemp)
if aws --endpoint-url https://storage.yandexcloud.net s3api head-object \
  --bucket "$STAGING_STATE_BUCKET" \
  --key snaphost/staging/terraform.tfstate 2>"$state_error"; then
  rm -f -- "$state_error"
  echo 'staging state already exists; refusing first plan' >&2
  exit 1
elif ! grep -Eq '\(404\)|NoSuchKey|Not Found' "$state_error"; then
  rm -f -- "$state_error"
  echo 'cannot prove staging state absence; refusing first plan' >&2
  exit 1
fi
rm -f -- "$state_error"
mkdir -p plans
verify_staging_state_boundary
terraform plan \
  -var-file=environments/staging.tfvars \
  -out=plans/staging.tfplan
terraform show -no-color plans/staging.tfplan
```

The owner must manually review folder IDs, domains, names, IAM grants, Lockbox
binding, DNS/certificate resources, destructive actions, and every replacement.
Only after approval may an operator later run:

```bash
terraform apply plans/staging.tfplan
```

Repository automation does not run this command. The first manually reviewed
bootstrap apply and its recovery evidence are recorded below; future applies
must repeat the same identity, boundary, and plan review gates.

## First reviewed bootstrap plan

The first real staging plan was generated on 2026-07-04 from the dedicated
working copy and empty state key. It contains 21 creates, 0 changes, and 0
destroys. The reviewed resource types are DNS zone/records, wildcard
certificate, API Gateway with the dummy 404 specification, registry/repository
and lifecycle policy, builder/runner service accounts and authorized keys,
their IAM bindings, and protected local key files. All known folder, domain,
and resource names are staging-only. No production identifier or private key
material appears in the plan JSON.

`router_image_url` and Lockbox references are intentionally empty in this
bootstrap plan, so no router Serverless Container is planned. Router enablement
requires a second reviewed plan after the registry exists and an immutable
router image has been pushed.

The state buckets are private, versioned, and encrypted with separate KMS keys,
and backend identities cannot access the other environment's bucket. The fixed
Yandex HTTPS endpoint was accepted as the TLS control in
[ADR 0005](../decisions/0005-terraform-state-backends.md). The incompatible
AWS `aws:SecureTransport` policies were removed and backend access was
restored. Object Storage access logging still returns `AccessDenied`. The owner
approved an exception for the first staging apply on 2026-07-05. This exception
does not apply to production: access logging must be enabled and verified for
both environment state buckets before any production apply, as tracked in the
[backlog](../tasks/backlog.md).

### First apply evidence (2026-07-05)

The owner approved the access-logging exception for the first staging apply.
The fail-closed gate passed before apply: the backend access key belonged to
the expected staging service account, the staging bucket was accessible, the
production bucket was denied, and the saved plan still contained 21 creates,
0 changes, and 0 destroys with no production identifiers.

The first apply was partial. Seventeen resources were confirmed in remote
state, including the registry/repository/lifecycle policy, certificate,
builder and runner service accounts and keys, IAM bindings, and protected
local key files. Creation of `staging.snaphost.online` failed because Yandex
requires permission on the existing parent public zone even when the child is
created in another folder.

To preserve environment isolation, the staging bootstrap account was not
granted access to the parent zone. The operator profile created the child zone
in the staging folder and it was successfully imported into the staging state.
A reviewed follow-up plan then contained three creates, one provider
normalization update, and no destroys.

The apparent CLI outage was caused by inherited `HTTP_PROXY`, `HTTPS_PROXY`,
and `ALL_PROXY` environment variables. With both uppercase and lowercase proxy
variables removed for the command process, Yandex API and Terraform backend
requests completed normally. The direct apply created the certificate
validation record and API Gateway, leaving 20 resources in state, but attaching
the original staging custom domain failed because Yandex reported its domain
object as already attached to the existing production API Gateway. Terraform
marked the staging gateway tainted and did not create the wildcard gateway DNS
record.

The conflict was resolved with the dedicated staging domain `kinocassa.ru`,
delegated to Yandex Cloud DNS. Recovery was intentionally split into two
reviewed applies: first the DNS zone, managed certificate, and validation
record were replaced; after the certificate reached `ISSUED`, the tainted API
Gateway was replaced and `*.kinocassa.ru` was created. Final evidence: 21
resources in remote state, a zero-drift Terraform plan, public wildcard CNAME
resolution to the new gateway, and HTTPS `404` from `probe.kinocassa.ru` as
expected from the bootstrap dummy route. Saved plans from the failed domain
attempt must not be reused.

## Staging VDS contract

Use a dedicated Ubuntu 24.04 LTS VDS. A practical initial minimum for the full
control plane and BuildKit is 4 vCPU, 8 GiB RAM, and 80 GiB SSD, with capacity
adjusted from observed builds and backup size. Install Docker Engine, Docker
Compose v2, Bash 4+, `flock`, `curl`, and core utilities used by `deploy.sh`.

Create a non-root `deployer` account with UID/GID `1000:1000`. It owns
`/opt/snaphost` and needs Docker daemon access; Docker group membership is
root-equivalent on that staging VDS,
so the account must have a dedicated SSH key and no general administrative use.
Do not enable root SSH or copy production credentials.

Ubuntu 24.04's AppArmor user-namespace restriction blocks the rootless
BuildKit container unless the named profile from
`infra/apparmor/snaphost-buildkit-rootless` is installed by root as described
in [production deployment](production-deployment.md#ubuntu-2404-rootless-buildkit-prerequisite).
The first live staging deployment exposed this as
`fork/exec /proc/self/exe: permission denied`. Assigning the versioned profile
to the container made `buildctl debug workers` succeed and the container become
healthy without weakening the host-wide AppArmor setting.

An actual Dockerfile `RUN` then exposed Docker's separate system-path masking:
the nested rootless OCI executor could not mount procfs and failed with
`error mounting "proc" ... operation not permitted`. Production Compose now
sets `systempaths=unconfined` only on `buildkitd`, matching the upstream
rootless BuildKit container contract. A healthy daemon alone is insufficient;
staging proof must include a Dockerfile with at least one `RUN` instruction.

The next rollout exposed a second host/image boundary: the original builder
image created `snaphost` with a dynamic system UID and therefore could not read
the deployer's `0600` bind-mounted service-account key. The image now fixes its
non-root identity at `1000:1000`, matching this provisioning contract. Secret
permissions remain `0400/0600`; making the key group/world-readable is not an
accepted workaround.

The bootstrap gateway intentionally returns a dummy `Deployment not found`
until all router inputs are configured. Before testing user URLs, push an
immutable `router-svc` image, create a deletion-protected Lockbox version whose
`WEBHOOK_SECRET` matches the control plane, populate all four router inputs,
and apply the reviewed plan. Verify the live gateway specification contains a
`serverless_containers` integration rather than the dummy response. A previous
successful user deploy cannot validate this after its TTL expires because the
watchdog removes both the container and its running route.

Firewall policy:

- SSH only from explicit operator/GitHub runner egress allowlists;
- public ingress only on HTTPS for the staging control-plane endpoint;
- no public PostgreSQL, Redis, BuildKit, user-billing, or Docker daemon;
- no direct public API gateway container port bypassing the TLS endpoint;
- outbound access only as required for GHCR, Yandex APIs/registry, OpenRouter,
  package/base-image retrieval, and system updates.

Create environment-owned paths:

```text
/opt/snaphost                             0751 (caddy traverse; no listing)
/opt/snaphost/env/production.env          0400 or 0600
/opt/snaphost/secrets/                    0700; files 0400 or 0600
/opt/snaphost/state/                      0700
/opt/snaphost/backups/                    0700
/opt/snaphost/releases/                   deployer-owned
/opt/snaphost/frontend/                   0755 (Caddy serves current/)
```

`/opt/snaphost` must be `0751`, not `0750`: the host Caddy runs as the `caddy`
user and serves `frontend/current`. With `0750` the `caddy` user cannot traverse
into `frontend/` and every request returns `403`. `0751` grants directory
traversal only — no directory listing — while `secrets/`, `env/`, `state/`, and
`backups/` remain `0700` and unreadable by `caddy`.

The environment file's `SUPABASE_URL` must be the public Supabase project URL
(`https://<ref>.supabase.co`), never a local development Kong address such as
`http://supabase_kong_snaphost-ui:8000`. api-gateway prefetches JWKS from
`${SUPABASE_URL}/auth/v1/.well-known/jwks.json` at startup and exits fatally
(crash loop, failed HTTP readiness) if that host is unreachable.

Use a stable staging Compose project such as `snaphost-staging`. The separate
VDS, project name, env, secrets, state, backups, releases, Docker volumes,
domain, Yandex folder, registry, service accounts, Lockbox secret, gateway,
router, DNS, and certificate must never be shared with production.

### Service-account keys — source and verification

The `builder` and `runner` service-account authorized keys are
environment-scoped: a staging key only has rights on the staging registry and
staging Serverless Containers. Deploying a **production** key to a staging host
does not warn — it fails closed at the first Yandex call (registry push returns
`403 Forbidden`). This happened during the 2026-07-08 staging rebuild.

Source of truth for each environment's keys is Terraform, never a hand-picked
file from a shared `secrets/` root:

- staging keys resolve to `terraform/yandex/.keys/staging/{builder,runner}.json`
  (`key_output_dir = ".keys/staging"`); production keys to
  `terraform/yandex/.keys/{builder,runner}.json`. The Terraform outputs
  `builder_key_path` / `runner_key_path` point at the exact files, and
  `builder_sa_id` / `runner_sa_id` give the expected owners.
- On an S3 backend these files exist only in the working copy at apply time. If
  they are absent, regenerate an authorized key for the correct service account
  (via Terraform or the IAM API) — do not substitute the other environment's
  file.

Before starting services, verify each mounted key belongs to the intended
environment by comparing its `service_account_id` to the Terraform `*_sa_id`
output:

```bash
grep -o '"service_account_id": "[^"]*"' /opt/snaphost/secrets/builder-key.json
grep -o '"service_account_id": "[^"]*"' /opt/snaphost/secrets/runner-key.json
# must equal terraform output builder_sa_id / runner_sa_id for THIS environment
```

This check is now automated: `deploy.sh preflight` performs the same comparison
against `YANDEX_BUILDER_SA_ID` / `YANDEX_RUNNER_SA_ID` and refuses to deploy on
a mismatch, before Docker login or any Yandex call. Both variables are
mandatory in every environment's env file. The manual commands above stay
useful for inspecting a host you have not deployed to yet.

Local key storage is split per environment under `secrets/production/` and
`secrets/staging/`, with no key file at the root. See
[credentials.md](credentials.md) and
[Task 12](../tasks/active/0012-credential-separation.md).

## Values required from the owner

- Yandex `cloud_id`;
- staging-only `folder_id`;
- staging apex/wildcard domain and registrar delegation access;
- dedicated staging and production state bucket names;
- a staging-only backend service account/access-key pair, its non-secret
  service account ID for `EXPECTED_STAGING_STATE_SERVICE_ACCOUNT_ID`, and a
  policy that explicitly grants no production-bucket access;
- a separately owned production-only backend identity with no staging-bucket
  access;
- staging state key plus proof that both buckets have versioning, encryption,
  public-access blocking and the accepted fixed HTTPS endpoint; access logging
  may use the approved first-staging-apply exception but remains mandatory
  before any production apply;
- staging VDS IP/HTTPS domain and SSH allowlist sources;
- local path to a staging-scoped bootstrap key;
- staging Lockbox secret ID, version ID, and key name, never the secret value;
- immutable router image URL tagged with a 40-character SHA;
- environment-specific resource names from `staging.tfvars.example`;
- GitHub `staging` Environment SSH secrets and deployment variables listed in
  [staging deployment](staging-deployment.md).

Contract preparation, bootstrap apply, and state reconciliation are complete.
The staging VDS, HTTPS endpoint, firewall, and GitHub Environment values are
configured. The first deployment reached the VDS but stopped before migrations
because Ubuntu AppArmor blocked rootless BuildKit. The named-profile correction
is proven on that VDS; a clean exact-SHA workflow rerun remains required.
