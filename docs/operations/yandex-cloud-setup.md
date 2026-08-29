# Настройка SnapHost в Yandex Cloud

Status: Current
Type: Operations
Updated: 2026-07-03

Yandex Cloud сейчас используется как первый production runtime adapter для
SnapHost, а не как обязательная платформа для всего проекта. Общая архитектура
остаётся provider-agnostic: Docker backend работает локально, Yandex backend
включается через `RUNNER_BACKEND=yandex`, а будущие AWS/GCP/VK adapters должны
подключаться через те же границы backend/provider.

Подробный runtime-контракт по ключам и env описан в
[`operations/yandex-runtime.md`](yandex-runtime.md). Этот документ описывает
инфраструктурную настройку и smoke-проверку Yandex adapter.

## 0. Предварительные требования

- Аккаунт Yandex Cloud с подключённым биллингом.
- Доменное имя, зарегистрированное у любого регистратора.
- Terraform >= 1.7.
- Установленный `yc` CLI, авторизованный от имени владельца облака.
- Docker CLI для ручной проверки push/deploy.

## 1. Bootstrap service account

Terraform авторизуется через bootstrap service account. Он создаётся вручную
один раз, потому что должен существовать до запуска Terraform. Этот ключ нужен
только оператору/Terraform и не должен попадать в runtime контейнеры SnapHost.

```bash
yc iam service-account create \
  --name snaphost-bootstrap \
  --description "Bootstrap account for Terraform"

BOOTSTRAP_ID=$(yc iam service-account get snaphost-bootstrap --format json | jq -r .id)
FOLDER_ID=$(yc config get folder-id)

yc resource-manager folder add-access-binding "$FOLDER_ID" \
  --role admin \
  --subject "serviceAccount:${BOOTSTRAP_ID}"

mkdir -p terraform/bootstrap
yc iam key create \
  --service-account-id "$BOOTSTRAP_ID" \
  --output terraform/bootstrap/key.json

chmod 600 terraform/bootstrap/key.json
```

`terraform/bootstrap/` должен оставаться в `.gitignore`.

## 2. Terraform state and environment selection

The same Terraform root has identical resource addresses for each environment,
so backend state is the primary safety boundary. Production and staging must
use dedicated, mutually inaccessible state buckets and backend identities,
separate clean working copies, different `folder_id` and domain, and
environment-specific resource names. Different keys in one bucket are not an
acceptable isolation boundary.
Never point staging variables at production state or migrate production state
while preparing staging.

The reproducible staging init/plan procedure and the separate future production
state migration plan are documented in
[staging infrastructure provisioning](staging-provisioning.md). Always use a
saved plan and manual review. `terraform apply` is an operator-only future step,
not a CI action.

После `apply` сохраните outputs без публикации секретов:

```bash
terraform output -json > ../outputs.json
```

Нужные runtime outputs:

- `folder_id`
- `runner_sa_id`
- `api_gateway_id`
- `registry_url`
- `runner_key_path` и `builder_key_path` как sensitive outputs

Terraform state, backend credentials/config, plan files, generated `.keys/`,
bootstrap keys, and outputs must not enter Git. State contains service-account
private key material and requires secret-grade backend protection.

## 3. DNS и сертификат

Terraform создаёт публичную DNS-зону, DNS validation record для wildcard
certificate и wildcard CNAME для API Gateway. Делегирование домена у
регистратора всё равно делается вручную.

Получите NS-серверы зоны через Yandex Console или `yc`:

```bash
yc dns zone list
yc dns zone get <zone_id>
```

Укажите возвращённые `ns?.yandexcloud.net` как authoritative nameservers для
домена в панели регистратора. Проверка:

```bash
dig +short NS <domain>
dig +short CNAME "*.<domain>"
```

Yandex Certificate Manager выпускает wildcard certificate через DNS-01.
Переходите дальше только после статуса `VALID`:

```bash
yc certificate-manager certificate list
```

## 4. Runtime credentials

Для production runtime используются authorized-key JSON files, а не вручную
созданные IAM tokens.

- `snaphost-builder` / `builder-key.json`: только push в Container Registry.
- `snaphost-runner` / `runner-key.json`: управление Serverless Containers,
  API Gateway, pull из registry, invocation/log access и
  `iam.serviceAccounts.user` для запуска revision от runner SA.
- Bootstrap/admin key не монтируется в backend services.

Храните ключи вне репозитория, например:

```bash
scp secrets/runner-key.json prod:/opt/snaphost/secrets/runner-key.json
scp secrets/builder-key.json prod:/opt/snaphost/secrets/builder-key.json
ssh prod "chmod 600 /opt/snaphost/secrets/*.json"
```

В контейнеры ключи монтируются read-only, внутри контейнера обычно как
`/secrets/runner-key.json` и `/secrets/builder-key.json`.

## 5. Env для backend services

Runner API и runner watchdog:

```env
RUNNER_BACKEND=yandex
YANDEX_ROUTING_MODE=router
YANDEX_SA_KEY_PATH=/secrets/runner-key.json
YANDEX_FOLDER_ID=<terraform output folder_id>
YANDEX_RUNNER_SA_ID=<terraform output runner_sa_id>
YANDEX_API_GATEWAY_ID=<terraform output api_gateway_id>
YANDEX_REGISTRY_URL=<terraform output registry_url>
REGISTRY_ALLOWED_PREFIXES=<terraform output registry_url>
STRICT_IMAGE_VALIDATION=true
DOMAIN_SUFFIX=<your domain>
```

Production registry hardening values must stay narrow:

```env
STRICT_IMAGE_VALIDATION=true
REGISTRY_ALLOWED_PREFIXES=cr.yandex/<registry_id>/snaphost
YANDEX_REGISTRY_URL=cr.yandex/<registry_id>/snaphost
REGISTRY_AUTH_MODE=yandex_iam
REGISTRY_INSECURE=false
SCAN_FAIL_ON_CRITICAL=true
```

Central router (`router-svc`) is deployed by Task 10.6c Terraform as a Yandex
Serverless Container. It uses `YANDEX_AUTH_MODE=metadata`, the attached runner
service account, and `WEBHOOK_SECRET` from Yandex Lockbox. Do not put the
webhook secret value in `terraform.tfvars`; set only the Lockbox secret ID,
version ID, and key name:

```hcl
router_image_url                 = "cr.yandex/<registry_id>/snaphost/router-svc:<tag>"
router_user_billing_url          = "https://<durable-control-plane-api-host>"
router_webhook_secret_id         = "<lockbox-secret-id>"
router_webhook_secret_version_id = "<lockbox-secret-version-id>"
router_webhook_secret_key        = "WEBHOOK_SECRET"
```

Despite its retained backward-compatible name, `router_user_billing_url` is
the public HTTPS base URL of API gateway/control plane, not a directly exposed
user-billing service. Router appends `/internal/routes`; API gateway exposes
only that exact secret-protected route and forwards it internally. Use the
durable TLS hostname, not a temporary VDS smoke URL. Firewall allowlisting and
rate limiting provide defense in depth. The secret value remains in Lockbox and
must not be placed in `terraform.tfvars`.

Builder worker:

```env
REGISTRY_URL=<terraform output registry_url>
REGISTRY_AUTH_MODE=yandex_iam
YANDEX_SA_KEY_PATH=/secrets/builder-key.json
REGISTRY_INSECURE=false
SCAN_FAIL_ON_CRITICAL=true
```

Не используйте один и тот же `YANDEX_SA_KEY_PATH` для builder и runner в одном
process env: это разные сервисы и разные service accounts.

## 6. Runner image с Yandex backend

Yandex backend находится за build tag. Для production/smoke runner image
собирается так:

```bash
docker build -f snaphost-backend/runner-svc/Dockerfile \
  --build-arg GO_BUILD_TAGS=yandex \
  -t snaphost-runner-svc:yandex \
  snaphost-backend
```

Обычная Docker/dev сборка без `GO_BUILD_TAGS=yandex` должна продолжать
работать.

## 7. Smoke test центрального router mode

Production использует `YANDEX_ROUTING_MODE=router`: Terraform направляет
wildcard-маршрут API Gateway на `router-svc`, а runner не изменяет gateway spec
для отдельных deploy. Image tag должен совпадать с `deploy_id`.

Минимальная последовательность:

```bash
DEPLOY_ID=00000000-0000-4000-8000-000000001004
IMAGE=<registry_url>/smoke-104:${DEPLOY_ID}

# login делайте через builder service account / authorized key flow,
# не через runtime runner key
docker build -t "${IMAGE}" path/to/smoke-app
docker push "${IMAGE}"

curl -X POST http://localhost:<runner_port>/internal/deploys \
  -H "Content-Type: application/json" \
  -H "X-Webhook-Secret: <secret>" \
  -d '{
    "deploy_id": "'${DEPLOY_ID}'",
    "user_id": "smoke-user",
    "image_ref": "'${IMAGE}'",
    "subdomain": "proj-00000000000040008000000000001004",
    "port": 8080,
    "env": {},
    "ttl_seconds": 900
  }'
```

Ожидаемый результат:

- Serverless Container появился в Yandex Console.
- Billing содержит running deploy и правильный `container_id`.
- Публичный wildcard URL проходит через `router-svc` и возвращает HTTP 200.
- `DELETE /internal/deploys/<deploy_id>` возвращает 204.
- После cleanup URL возвращает 404, а другие deploy продолжают работать.
- TTL expiry через watchdog также удаляет container и заполняет `stopped_at`.

## 8. Terraform state

The root declares an S3-compatible Yandex Object Storage backend whose bucket
and key are supplied with environment-specific `-backend-config`. Staging and
production require separate buckets and separate service-account access keys;
each identity must have no access to the other environment's bucket. Backend
credentials come from `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`, not
committed files. Both buckets require versioning, server-side encryption,
public-access blocking, and audit/access logging. The fixed
`https://storage.yandexcloud.net` backend endpoint is the accepted TLS control;
see [ADR 0005](../decisions/0005-terraform-state-backends.md). Do not apply the
incompatible AWS `aws:SecureTransport` policy to these Yandex buckets.

Before every `terraform state list` and `terraform plan`, the gate must compare
the access key's JSON `service_account_id` with the explicitly supplied,
non-secret expected service account ID, positively verify access to the
intended bucket, and verify that the same credentials cannot access the other
environment's bucket. The concrete fail-closed command sequence is in
[staging provisioning](staging-provisioning.md). Never reuse one `.terraform/`
directory to switch environments. Existing production local state is not
automatically migrated; follow the reviewed migration procedure before changing
its backend.

## 9. Troubleshooting

| Симптом | Вероятная причина | Исправление |
| --- | --- | --- |
| `permission denied: container-registry.images.puller` | У runner SA нет registry pull роли | Проверьте `service_accounts.tf` и выполните `terraform apply` |
| `service account is not available` при deploy revision | Не хватает `iam.serviceAccounts.user` | Выполните актуальный Terraform apply |
| `Environment variable PORT is forbidden` | В revision явно передали `PORT` | `PORT` задаёт платформа; backend не должен добавлять его в env |
| Сертификат долго в `VALIDATING` | Домен не делегирован на Yandex DNS | Проверьте NS у регистратора и DNS propagation |
| `NXDOMAIN` для `*.domain` | Wildcard CNAME не создан или зона не делегирована | Проверьте `yandex_dns_recordset.wildcard_gateway` и NS |
| Terraform видит неожиданный drift API Gateway spec | Runner запущен в legacy `gateway` mode или spec изменён вручную | Для production установите `YANDEX_ROUTING_MODE=router`; Terraform должен владеть статическим маршрутом на `router-svc` |
| Постоянный 502 от Gateway | Route указывает на нерабочий container/revision | Проверьте runner logs, container revision и Cloud Logging |
