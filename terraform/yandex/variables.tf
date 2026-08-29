variable "bootstrap_key_file" {
  description = "Path to the authorized key JSON for the bootstrap service account that creates everything."
  type        = string
}

variable "cloud_id" {
  description = "Yandex Cloud ID."
  type        = string
}

variable "folder_id" {
  description = "Yandex Cloud folder ID where resources will be created."
  type        = string
}

variable "default_zone" {
  description = "Default availability zone."
  type        = string
  default     = "ru-central1-a"
}

# Serves user deploys (proj-<id>.<domain_name>) and the custom-domain edge.
# Kept separate from control_plane_domain on purpose — see the domain split in
# docs/architecture/deployment-model.md. Changing it recreates the DNS zone, the
# wildcard certificate, and the API Gateway domain binding, and invalidates
# every previously issued deploy URL.
variable "domain_name" {
  description = "Apex domain name (e.g. snaphost.app)."
  type        = string
}

variable "control_plane_domain" {
  description = <<-EOT
    Registrable domain for the dashboard and public API (for example
    snaphost.ru). Deliberately different from domain_name, which serves user
    deploys: cookies, storage partitions, and blocklist reputation are all
    scoped per registrable domain, so user-controlled content must not share
    the domain sessions live on. Empty skips the zone and its records.
  EOT
  type        = string
  default     = ""
}

variable "control_plane_api_label" {
  description = "Label under control_plane_domain that serves the public API."
  type        = string
  default     = "api"
}

variable "control_plane_auth_label" {
  description = "Label under control_plane_domain that serves self-hosted Supabase Auth."
  type        = string
  default     = "auth"
}

variable "control_plane_auth_ip_address" {
  description = "Static public IPv4 of the dedicated self-hosted Supabase VDS. Empty disables the Auth record."
  type        = string
  default     = ""

  validation {
    condition = var.control_plane_auth_ip_address == "" || try(
      cidrhost("${var.control_plane_auth_ip_address}/32", 0) == var.control_plane_auth_ip_address,
      false,
    )
    error_message = "control_plane_auth_ip_address must be empty or an IPv4 address."
  }
}

variable "edge_ip_address" {
  description = <<-EOT
    Static public IPv4 of the SnapHost edge host — the address users point their
    own domains at (Task 16 P2). Published as an A record at edge.<domain_name>
    so the platform can move hosts without breaking every customer that used a
    CNAME; only apex domains, which cannot hold a CNAME, depend on the bare
    address. Changing this value re-points live customer traffic: treat it as a
    contract, not a config knob. Empty disables the record.
  EOT
  type        = string
  default     = ""
}

variable "edge_hostname_label" {
  description = "Label under domain_name that resolves to edge_ip_address."
  type        = string
  default     = "edge"
}

variable "environment" {
  description = "Deployment environment label (prod, staging, ...)."
  type        = string
  default     = "prod"
}

variable "api_gateway_name" {
  description = "Environment-specific API Gateway name. Production-compatible default preserves the existing resource name."
  type        = string
  default     = "snaphost-router"
}

variable "certificate_name" {
  description = "Environment-specific wildcard certificate name. Production-compatible default preserves the existing resource name."
  type        = string
  default     = "snaphost-wildcard"
}

variable "router_container_name" {
  description = "Environment-specific central router container name. Production-compatible default preserves the existing resource name."
  type        = string
  default     = "snaphost-router-svc"
}

variable "registry_cleanup_policy_name" {
  description = "Environment-specific registry cleanup policy name. Production-compatible default preserves the existing resource name."
  type        = string
  default     = "snaphost-cleanup"
}

variable "key_output_dir" {
  description = "Repository-local ignored directory for generated runtime authorized keys. Use a distinct subdirectory per environment."
  type        = string
  default     = ".keys"

  validation {
    condition     = can(regex("^\\.keys(/[a-z0-9][a-z0-9_-]*)?$", var.key_output_dir))
    error_message = "key_output_dir must be .keys or one lowercase child such as .keys/staging."
  }
}

variable "runner_sa_name" {
  description = "Name of the service account used by runner-svc."
  type        = string
  default     = "snaphost-runner"
}

variable "builder_sa_name" {
  description = "Name of the service account used by builder-svc."
  type        = string
  default     = "snaphost-builder"
}

variable "registry_name" {
  description = "Container registry name."
  type        = string
  default     = "snaphost"
}

variable "router_image_url" {
  description = "Router-svc image URL for the static central router Serverless Container. Leave empty to keep the gateway dummy 404 spec."
  type        = string
  default     = ""
}

variable "router_user_billing_url" {
  description = "Public HTTPS API gateway/control-plane base URL used by router-svc for the exact /internal/routes lookup. The retained name is backward compatible; do not expose user-billing directly. Leave empty when router_image_url is empty."
  type        = string
  default     = ""
}

variable "router_webhook_secret_id" {
  description = "Yandex Lockbox secret ID containing WEBHOOK_SECRET for router-svc. The secret value itself must not be stored in Terraform."
  type        = string
  default     = ""
}

variable "router_webhook_secret_version_id" {
  description = "Yandex Lockbox secret version ID containing WEBHOOK_SECRET for router-svc."
  type        = string
  default     = ""
}

variable "router_webhook_secret_key" {
  description = "Key inside the Lockbox secret version that contains the router WEBHOOK_SECRET value."
  type        = string
  default     = "WEBHOOK_SECRET"
}

variable "router_proxy_timeout_sec" {
  description = "router-svc upstream proxy timeout in seconds."
  type        = number
  default     = 60
}

variable "router_token_cache_ttl_sec" {
  description = "router-svc in-process IAM token cache TTL in seconds."
  type        = number
  default     = 600
}

variable "router_memory_mb" {
  description = "Memory for the router-svc Serverless Container, in MB."
  type        = number
  default     = 512
}

variable "router_cores" {
  description = "CPU cores for the router-svc Serverless Container."
  type        = number
  default     = 1
}

variable "router_core_fraction" {
  description = "CPU core fraction for the router-svc Serverless Container."
  type        = number
  default     = 50
}

variable "runtime_log_group_name" {
  description = "Name of the log group collecting user container stdout/stderr."
  type        = string
  default     = "snaphost-runtime"
}

variable "runtime_log_retention" {
  description = "How long user container logs are kept. Untrusted, high-volume output that stops being useful once the deploy is gone."
  type        = string
  default     = "168h"
}
