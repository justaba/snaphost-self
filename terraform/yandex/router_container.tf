locals {
  router_config_values = [
    var.router_image_url,
    var.router_user_billing_url,
    var.router_webhook_secret_id,
    var.router_webhook_secret_version_id,
    var.router_webhook_secret_key,
  ]

  router_enabled = alltrue([for v in local.router_config_values : trimspace(v) != ""])
  router_disabled = alltrue([
    trimspace(var.router_image_url) == "",
    trimspace(var.router_user_billing_url) == "",
    trimspace(var.router_webhook_secret_id) == "",
    trimspace(var.router_webhook_secret_version_id) == "",
  ])
}

resource "yandex_lockbox_secret_iam_member" "router_webhook_secret_reader" {
  count     = local.router_enabled ? 1 : 0
  secret_id = var.router_webhook_secret_id
  role      = "lockbox.payloadViewer"
  member    = "serviceAccount:${yandex_iam_service_account.runner.id}"
}

resource "yandex_serverless_container" "router" {
  count              = local.router_enabled ? 1 : 0
  name               = var.router_container_name
  description        = "SnapHost central runtime router. API Gateway sends wildcard deploy traffic here."
  folder_id          = var.folder_id
  memory             = var.router_memory_mb
  cores              = var.router_cores
  core_fraction      = var.router_core_fraction
  execution_timeout  = "${var.router_proxy_timeout_sec}s"
  service_account_id = yandex_iam_service_account.runner.id

  labels = {
    managed_by = "snaphost"
    component  = "router-svc"
  }

  runtime {
    type = "http"
  }

  metadata_options {
    gce_http_endpoint    = 1
    aws_v1_http_endpoint = 2
  }

  image {
    url = var.router_image_url
    environment = {
      DOMAIN_SUFFIX       = var.domain_name
      USER_BILLING_URL    = var.router_user_billing_url
      YANDEX_AUTH_MODE    = "metadata"
      PROXY_TIMEOUT_SEC   = tostring(var.router_proxy_timeout_sec)
      TOKEN_CACHE_TTL_SEC = tostring(var.router_token_cache_ttl_sec)
    }
  }

  secrets {
    id                   = var.router_webhook_secret_id
    version_id           = var.router_webhook_secret_version_id
    key                  = var.router_webhook_secret_key
    environment_variable = "WEBHOOK_SECRET"
  }

  depends_on = [
    yandex_lockbox_secret_iam_member.router_webhook_secret_reader,
  ]
}
