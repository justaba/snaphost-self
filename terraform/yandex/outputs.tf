output "folder_id" {
  description = "Yandex folder ID."
  value       = var.folder_id
}

output "registry_id" {
  description = "Container registry ID."
  value       = yandex_container_registry.snaphost.id
}

output "registry_url" {
  description = "Full registry URL prefix to use as REGISTRY_URL / YANDEX_REGISTRY_URL."
  value       = "cr.yandex/${yandex_container_registry.snaphost.id}/snaphost"
}

output "dns_zone_id" {
  description = "Public DNS zone ID."
  value       = yandex_dns_zone.main.id
}

output "certificate_id" {
  description = "Wildcard certificate ID."
  value       = yandex_cm_certificate.wildcard.id
}

output "control_plane_zone_id" {
  description = "Control-plane DNS zone ID (dashboard + API), or null when no control-plane domain is configured."
  value       = local.control_plane_enabled ? yandex_dns_zone.control_plane[0].id : null
}

output "control_plane_api_host" {
  description = "Public API hostname — use as VITE_API_URL host and in CORS_ALLOW_ORIGINS' sibling dashboard origin."
  value       = local.control_plane_enabled ? "${var.control_plane_api_label}.${var.control_plane_domain}" : ""
}

output "control_plane_auth_host" {
  description = "Self-hosted Supabase Auth hostname, or empty when its dedicated address is not configured."
  value       = local.control_plane_enabled && var.control_plane_auth_ip_address != "" ? "${var.control_plane_auth_label}.${var.control_plane_domain}" : ""
}

output "edge_hostname" {
  description = "Hostname customers CNAME their domains to (DOMAIN_CNAME_TARGET). Empty when no edge address is reserved."
  value       = var.edge_ip_address == "" ? "" : "${var.edge_hostname_label}.${var.domain_name}"
}

output "edge_ip_address" {
  description = "Static edge IPv4 customers with an apex domain point an A record at (DOMAIN_A_RECORD_TARGET)."
  value       = var.edge_ip_address
}

output "api_gateway_id" {
  description = "API Gateway ID — pass as YANDEX_API_GATEWAY_ID to runner-svc."
  value       = yandex_api_gateway.router.id
}

output "api_gateway_default_domain" {
  description = "API Gateway *.yandexcloud.net domain (used as CNAME target if you ever bypass custom domain)."
  value       = yandex_api_gateway.router.domain
}

output "router_container_id" {
  description = "Central router Serverless Container ID, or null when router infra wiring is disabled."
  value       = local.router_enabled ? yandex_serverless_container.router[0].id : null
}

output "router_container_url" {
  description = "Central router Serverless Container invocation URL, or null when router infra wiring is disabled."
  value       = local.router_enabled ? yandex_serverless_container.router[0].url : null
}

output "runner_sa_id" {
  description = "Runner service account ID — pass as YANDEX_RUNNER_SA_ID to runner-svc."
  value       = yandex_iam_service_account.runner.id
}

output "builder_sa_id" {
  description = "Builder service account ID."
  value       = yandex_iam_service_account.builder.id
}

output "runner_key_path" {
  description = "Local path to the runner authorized key JSON."
  value       = local_sensitive_file.runner_key.filename
  sensitive   = true
}

output "builder_key_path" {
  description = "Local path to the builder authorized key JSON."
  value       = local_sensitive_file.builder_key.filename
  sensitive   = true
}

output "runtime_log_group_id" {
  description = "Log group for user container stdout/stderr; passed to runner-svc as YANDEX_LOG_GROUP_ID."
  value       = yandex_logging_group.runtime.id
}
