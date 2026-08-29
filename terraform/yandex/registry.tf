resource "yandex_container_registry" "snaphost" {
  name      = var.registry_name
  folder_id = var.folder_id
}

resource "yandex_container_repository" "snaphost_proj" {
  name = "${yandex_container_registry.snaphost.id}/snaphost"
}

# Lifecycle policy: keep last 5 tagged images per repository, drop untagged
# images older than 7 days. Keeps registry size bounded between deploys.
resource "yandex_container_repository_lifecycle_policy" "cleanup" {
  name          = var.registry_cleanup_policy_name
  repository_id = yandex_container_repository.snaphost_proj.id
  status        = "active"

  rule {
    description   = "Keep last 5 images per project, delete untagged older than 7 days"
    expire_period = "168h"
    untagged      = true
    retained_top  = 5
  }
}
