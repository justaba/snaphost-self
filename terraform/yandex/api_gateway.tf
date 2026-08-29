resource "yandex_api_gateway" "router" {
  name        = var.api_gateway_name
  description = "Wildcard subdomain router for user deployments. In central-router mode the spec points to router-svc."

  custom_domains {
    fqdn           = "*.${var.domain_name}"
    certificate_id = yandex_cm_certificate.wildcard.id
  }

  spec = templatefile("${path.module}/api_gateway_spec.yaml", {
    domain_name          = var.domain_name
    router_enabled       = local.router_enabled
    router_container_id  = local.router_enabled ? yandex_serverless_container.router[0].id : ""
    router_invoker_sa_id = yandex_iam_service_account.runner.id
  })

  depends_on = [
    yandex_cm_certificate.wildcard,
    yandex_dns_recordset.cert_validation,
    yandex_serverless_container.router,
  ]

  lifecycle {
    precondition {
      condition     = local.router_enabled || local.router_disabled
      error_message = "Router configuration must be all-or-none: set router_image_url, router_user_billing_url, router_webhook_secret_id, router_webhook_secret_version_id, and router_webhook_secret_key together, or leave them empty."
    }
  }
}
