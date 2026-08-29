############################################
# Runner service account (least privilege) #
############################################

resource "yandex_iam_service_account" "runner" {
  name        = var.runner_sa_name
  description = "SnapHost runner-svc: deploy and invoke serverless containers, manage API gateway routes."
  folder_id   = var.folder_id
}

locals {
  runner_roles = [
    "container-registry.images.puller",
    "serverless-containers.containerInvoker",
    "serverless-containers.editor",
    "api-gateway.editor",
    # Required to attach this service account to Serverless Container revisions:
    "iam.serviceAccounts.user",
    # Required to read logs from default folder log group:
    "logging.reader",
    # Serverless Containers write a revision's stdout/stderr as the service
    # account attached to that revision, which is this one. Without it the
    # log_options on a revision are accepted and silently produce nothing.
    "logging.writer",
    # NOTE: folder-level logging.reader + logging.writer are NOT sufficient to
    # name a log group in a container revision. On 2026-08-04 DeployRevision
    # kept failing with "Not enough permissions to use log group <id>" with
    # both roles granted, and adding logging.viewer changed nothing after 15
    # minutes of propagation. Whatever the missing grant is, it is not a
    # folder-level logging role — most likely a binding on the log group
    # resource itself, which this provider version cannot express. Until that
    # is resolved, production runs with YANDEX_RUNTIME_LOGS_DISABLED=true and
    # containers log to the folder's default group.
  ]
}

resource "yandex_resourcemanager_folder_iam_member" "runner_roles" {
  for_each  = toset(local.runner_roles)
  folder_id = var.folder_id
  role      = each.value
  member    = "serviceAccount:${yandex_iam_service_account.runner.id}"
}

resource "yandex_iam_service_account_key" "runner" {
  service_account_id = yandex_iam_service_account.runner.id
  description        = "Authorized key for runner-svc IAM token exchange."
  key_algorithm      = "RSA_2048"
}

resource "local_sensitive_file" "runner_key" {
  filename = "${path.module}/${var.key_output_dir}/runner.json"
  content = jsonencode({
    id                 = yandex_iam_service_account_key.runner.id
    service_account_id = yandex_iam_service_account.runner.id
    created_at         = yandex_iam_service_account_key.runner.created_at
    key_algorithm      = yandex_iam_service_account_key.runner.key_algorithm
    public_key         = yandex_iam_service_account_key.runner.public_key
    private_key        = yandex_iam_service_account_key.runner.private_key
  })
  file_permission = "0600"
}

#############################################
# Builder service account (push only)       #
#############################################

resource "yandex_iam_service_account" "builder" {
  name        = var.builder_sa_name
  description = "SnapHost builder-svc: push images to container registry."
  folder_id   = var.folder_id
}

locals {
  builder_roles = [
    "container-registry.images.pusher",
  ]
}

resource "yandex_resourcemanager_folder_iam_member" "builder_roles" {
  for_each  = toset(local.builder_roles)
  folder_id = var.folder_id
  role      = each.value
  member    = "serviceAccount:${yandex_iam_service_account.builder.id}"
}

resource "yandex_iam_service_account_key" "builder" {
  service_account_id = yandex_iam_service_account.builder.id
  description        = "Authorized key for builder-svc IAM token exchange."
  key_algorithm      = "RSA_2048"
}

resource "local_sensitive_file" "builder_key" {
  filename = "${path.module}/${var.key_output_dir}/builder.json"
  content = jsonencode({
    id                 = yandex_iam_service_account_key.builder.id
    service_account_id = yandex_iam_service_account.builder.id
    created_at         = yandex_iam_service_account_key.builder.created_at
    key_algorithm      = yandex_iam_service_account_key.builder.key_algorithm
    public_key         = yandex_iam_service_account_key.builder.public_key
    private_key        = yandex_iam_service_account_key.builder.private_key
  })
  file_permission = "0600"
}
