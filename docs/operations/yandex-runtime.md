# Yandex Runtime Contract

Status: Current
Type: Operations
Updated: 2026-07-03

This document defines the credential, secret-file, and environment variable
contract for running SnapHost's first production runtime adapter against Yandex
Cloud. Yandex is an adapter behind the provider/runtime boundary, not a
platform-wide dependency. The Task 10 runtime implementation and smoke work is
complete; this document is the current operator-facing configuration contract.

## Credential Model

SnapHost uses two different credential classes in Yandex Cloud:

- Bootstrap Terraform credentials are provisioning-only credentials. They are
  used by Terraform to create infrastructure, service accounts, service-account
  keys, registry resources, DNS, certificates, and API Gateway resources.
- Production runtime credentials are service-account authorized-key JSON files
  mounted into backend service containers. Runtime services use these files to
  authenticate as the narrow service account assigned to that service.

Production backend services must receive service-account authorized-key JSON
files, not pre-created IAM tokens. IAM tokens are short-lived implementation
details. The Yandex SDK signs with the authorized key, exchanges for IAM tokens,
and refreshes tokens as needed. Operators should not run `yc iam create-token`
and inject that token into `builder-svc` or `runner-svc`.

Bootstrap/admin Terraform credentials must never be mounted into production
backend containers. They have provisioning power and belong only in the
Terraform execution environment.

## Service Accounts

Terraform under `terraform/yandex/` creates two runtime service accounts:

- `snaphost-builder` is used by `builder-svc`. It is only for pushing images to
  Yandex Container Registry. The current Terraform role set is
  `container-registry.images.pusher`.
- `snaphost-runner` is used by `runner-svc`. It manages Yandex Serverless
  Containers, API Gateway routes, registry pulls, invocation, and log access as
  needed. The current Terraform role set is `serverless-containers.editor`,
  `serverless-containers.containerInvoker`, `api-gateway.editor`,
  `container-registry.images.puller`, `iam.serviceAccounts.user`, and
  `logging.reader`. In central-router mode Terraform also grants
  `lockbox.payloadViewer` on the specific Lockbox secret that contains
  `WEBHOOK_SECRET`.

Neither runtime account should receive broad admin, billing admin, IAM admin,
DNS admin, or unrelated folder-wide privileges. Terraform/bootstrap credentials
remain the only credentials with provisioning-level access.

## Secret Files

Terraform writes authorized-key JSON files under `terraform/yandex/.keys/`:

- `builder_key_path` points to the generated builder key.
- `runner_key_path` points to the generated runner key.

Copy those files to the production host through a secure channel. Recommended
host paths:

```text
/opt/snaphost/secrets/builder-key.json
/opt/snaphost/secrets/runner-key.json
```

Recommended container mount paths:

```text
/secrets/builder-key.json
/secrets/runner-key.json
```

Set permissions to `0600`, or otherwise make the files readable only by the
deployment user/container runtime that needs them. Do not commit, paste, log,
print, expose through API responses, or include these files in environment
dumps.

Mount each key only into the service that needs it:

- `builder-worker` receives the builder key.
- `builder-api` normally does not receive the builder key if it only enqueues
  build jobs and never pushes images itself.
- `runner-api` receives the runner key for Yandex backend lifecycle operations.
- `runner-watchdog` receives the runner key for Yandex backend cleanup and TTL
  lifecycle operations.
- No runtime service receives the bootstrap/admin Terraform key.

## Environment Variables

Map Terraform outputs to backend environment variables as follows:

| Terraform output | Service | Env var |
| --- | --- | --- |
| `builder_key_path` | builder-svc | `YANDEX_SA_KEY_PATH` |
| `runner_key_path` | runner-svc | `YANDEX_SA_KEY_PATH` |
| `folder_id` | runner-svc | `YANDEX_FOLDER_ID` |
| `registry_url` | builder-svc | `REGISTRY_URL` |
| `registry_url` | runner-svc | `YANDEX_REGISTRY_URL` |
| `runner_sa_id` | runner-svc | `YANDEX_RUNNER_SA_ID` |
| `api_gateway_id` | runner-svc | `YANDEX_API_GATEWAY_ID` |
| `runtime_log_group_id` | runner-svc | `YANDEX_LOG_GROUP_ID` |
| `router_container_id` | smoke/docs | central router container ID |
| `router_container_url` | smoke/docs | central router direct invocation URL |

Production Yandex deployments should also set:

```env
RUNNER_BACKEND=yandex
REGISTRY_AUTH_MODE=yandex_iam
REGISTRY_INSECURE=false
SCAN_FAIL_ON_CRITICAL=true
STRICT_IMAGE_VALIDATION=true
REGISTRY_URL=<terraform output registry_url>
YANDEX_REGISTRY_URL=<terraform output registry_url>
REGISTRY_ALLOWED_PREFIXES=<terraform output registry_url>
```

`REGISTRY_ALLOWED_PREFIXES` must match the same Yandex registry prefix that
`builder-svc` pushes to, normally the `registry_url` Terraform output such as
`cr.yandex/<registry_id>/snaphost`. Do not add a broader prefix such as
`cr.yandex/<registry_id>`; strict runner validation should only accept images
under the `snaphost` repository path.

## Security Hardening

Runner stop/delete requests are checked against user-billing before any backend
cloud delete is attempted. The runner fetches the stored deploy mapping and
only calls the backend when the requested `container_id` exactly matches the
stored `container_id` for that `deploy_id`. Missing deploy rows, empty stored
container mappings, and mismatched IDs are rejected as validation failures.

SnapHost-created runtime resources carry ownership metadata where the backend
supports it:

- Docker containers carry `snaphost.deploy.managed_by=snaphost`,
  `snaphost.deploy.id=<deploy_id>`, and `snaphost.deploy.user_id=<user_id>`.
- Yandex Serverless Containers support labels on create requests and receive
  `managed_by=snaphost`, `deploy_id=<deploy_id>`, and `user_id=<user_id>`.
- API Gateway routes are represented inside the OpenAPI spec and do not expose
  a separate per-route label API in the current SDK path; route ownership is
  tied to the deploy hostname and container mapping in the spec.

## Compose Fragment

Example-only production-style fragment. Do not copy bootstrap/admin Terraform
keys into these mounts.

```yaml
services:
  builder-worker:
    environment:
      REGISTRY_URL: ${YANDEX_REGISTRY_URL}
      REGISTRY_AUTH_MODE: yandex_iam
      REGISTRY_INSECURE: "false"
      SCAN_FAIL_ON_CRITICAL: "true"
      YANDEX_SA_KEY_PATH: /secrets/builder-key.json
    volumes:
      - /opt/snaphost/secrets/builder-key.json:/secrets/builder-key.json:ro

  runner-api:
    environment:
      RUNNER_BACKEND: yandex
      YANDEX_ROUTING_MODE: router
      YANDEX_SA_KEY_PATH: /secrets/runner-key.json
      YANDEX_FOLDER_ID: ${YANDEX_FOLDER_ID}
      YANDEX_REGISTRY_URL: ${YANDEX_REGISTRY_URL}
      YANDEX_RUNNER_SA_ID: ${YANDEX_RUNNER_SA_ID}
      YANDEX_API_GATEWAY_ID: ${YANDEX_API_GATEWAY_ID}
      STRICT_IMAGE_VALIDATION: "true"
      REGISTRY_ALLOWED_PREFIXES: ${YANDEX_REGISTRY_URL}
    volumes:
      - /opt/snaphost/secrets/runner-key.json:/secrets/runner-key.json:ro

  runner-watchdog:
    environment:
      RUNNER_BACKEND: yandex
      YANDEX_SA_KEY_PATH: /secrets/runner-key.json
      YANDEX_FOLDER_ID: ${YANDEX_FOLDER_ID}
      YANDEX_REGISTRY_URL: ${YANDEX_REGISTRY_URL}
      YANDEX_RUNNER_SA_ID: ${YANDEX_RUNNER_SA_ID}
      YANDEX_API_GATEWAY_ID: ${YANDEX_API_GATEWAY_ID}
    volumes:
      - /opt/snaphost/secrets/runner-key.json:/secrets/runner-key.json:ro

  router-svc:
    environment:
      DOMAIN_SUFFIX: ${DOMAIN_SUFFIX}
      USER_BILLING_URL: https://control.example.com
      WEBHOOK_SECRET: ${WEBHOOK_SECRET}
      YANDEX_AUTH_MODE: key_file
      YANDEX_SA_KEY_PATH: /secrets/runner-key.json
      PROXY_TIMEOUT_SEC: "60"
      TOKEN_CACHE_TTL_SEC: "600"
    volumes:
      - /opt/snaphost/secrets/runner-key.json:/secrets/runner-key.json:ro
```

Use separate mounts even if both files live under the same host directory. That
keeps accidental cross-service key access visible in compose review.

`router-svc` is built from the `snaphost-backend/` Docker context so the
`shared/` module is available:

```bash
docker build -f router-svc/Dockerfile -t snaphost/router-svc .
```

Router environment contract:

- `DOMAIN_SUFFIX`: required public runtime domain suffix, for example
  `snaphost.pw`. Only one-label subdomains under this suffix are routed.
- `USER_BILLING_URL`: public HTTPS control-plane/API gateway base URL. Router
  appends `/internal/routes`; this must not point directly to user-billing or a
  temporary VDS smoke URL.
- `WEBHOOK_SECRET`: shared internal API secret for user-billing route lookup.
- `YANDEX_AUTH_MODE`: `key_file` for Docker/Compose style runtime with a
  mounted authorized-key JSON file; `metadata` for Yandex Serverless Container
  runtime with an attached service account.
- `YANDEX_SA_KEY_PATH`: path to the mounted runner service-account key when
  `YANDEX_AUTH_MODE=key_file`.
- `PROXY_TIMEOUT_SEC`: optional upstream proxy timeout in seconds.
- `TOKEN_CACHE_TTL_SEC`: optional in-process Yandex IAM token cache TTL in
  seconds.

When `YANDEX_ROUTING_MODE=router` is enabled, API Gateway must send wildcard
runtime traffic to `router-svc`. The runner should still create/delete
per-deploy Serverless Containers, but it must not mutate API Gateway routes per
deploy.

## Central Router Terraform Wiring

Task 10.6c wires the production router path through Yandex Serverless
Containers:

1. Build `router-svc` from the `snaphost-backend/` Docker context.
2. Push that image to the Yandex registry.
3. Store `WEBHOOK_SECRET` in Yandex Lockbox outside Terraform.
4. Set only non-secret identifiers in `terraform/yandex/terraform.tfvars`:

```hcl
router_image_url                 = "cr.yandex/<registry_id>/snaphost/router-svc:<tag>"
router_user_billing_url          = "https://<durable-control-plane-api-host>"
router_webhook_secret_id         = "<lockbox-secret-id>"
router_webhook_secret_version_id = "<lockbox-secret-version-id>"
router_webhook_secret_key        = "WEBHOOK_SECRET"
```

Terraform creates `yandex_serverless_container.router` with
`YANDEX_AUTH_MODE=metadata`, attaches the runner service account, grants that
service account `lockbox.payloadViewer` on the specific Lockbox secret, and
updates the API Gateway greedy wildcard route to the router container using
`x-yc-apigateway-integration: serverless_containers`.

Leave the router variables empty to keep the dummy 404 gateway spec. Do not put
the actual webhook secret value in Terraform variables, docs, state, or logs.
The retained variable name is backward compatible but now means the public API
gateway/control-plane base URL. A rename can be handled as a separate migration.
The endpoint requires TLS and the same `WEBHOOK_SECRET` that API gateway reads;
router receives it from Lockbox. Firewall allowlisting and rate limiting are
recommended additional controls.
If the Lockbox secret uses a custom KMS key, grant the runner service account
the matching KMS decrypt role on that key before applying.

## Operational Checklist

- Run Terraform from `terraform/yandex/` using the bootstrap/provisioning
  credentials.
- Record the required Terraform outputs: `builder_key_path`, `runner_key_path`,
  `folder_id`, `registry_url`, `runner_sa_id`, and `api_gateway_id`.
- Copy the generated service-account key files to the production host through a
  secure channel.
- Set key-file permissions to `0600` or equivalent restricted access.
- Mount the builder key only into `builder-svc` containers.
- Mount the runner key only into `runner-svc` containers.
- Confirm both `runner-api` and `runner-watchdog` receive the runner key in
  Yandex production mode.
- For central-router mode in Terraform/Yandex, confirm `router-svc` runs with
  `YANDEX_AUTH_MODE=metadata`, has the runner service account attached, and
  `runner-api` is started with `YANDEX_ROUTING_MODE=router`.
- For central-router mode, set `DOMAIN_SUFFIX` identically for `runner-api` and
  `router-svc`, and confirm API Gateway wildcard traffic targets `router-svc`.
- Set backend environment variables from Terraform outputs.
- Confirm runtime containers do not receive bootstrap/admin Terraform keys.
- Confirm no service logs, deploy logs, API responses, or environment dumps
  contain service-account key contents or IAM token values.

## Runtime Logs

Build and saga logs continue to use the existing Redis deploy log stream.
Runner-svc also publishes concise runtime lifecycle messages to the same
`logs:{deploy_id}` channel, including validation, backend start/failure, Yandex
container/revision progress, router/gateway routing mode, public URL readiness,
manual stop/delete, and watchdog TTL cleanup.

User-visible runner/backend errors are redacted, whitespace-compacted, and
length-limited before publication. Do not rely on deploy logs for raw provider
responses, IAM tokens, secret values, or environment dumps.

Yandex Cloud Logging stdout/stderr tailing is not part of the current MVP. The
Yandex backend emits lifecycle/failure messages first; full Cloud Logging stream
integration remains future runtime observability work.

## Smoke testing

Production changes must verify deploy, public routing, manual delete, watchdog
TTL cleanup, invalid image tag, forbidden registry prefix, and missing-key
startup failure. Exact historical Task 10 smoke evidence is retained in
[`../tasks/archive/implementation-log.md`](../tasks/archive/implementation-log.md).
The reproducible automated version of this flow is part of
[`../tasks/active/0011-production-deployment.md`](../tasks/active/0011-production-deployment.md).
