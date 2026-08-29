# Control-plane DNS: the dashboard and the public API.
#
# A separate registrable domain from the one user deploys answer on, and that
# separation is the point rather than a preference. Browsers scope cookies and
# storage per registrable domain, so a deployed application sharing it could set
# cookies the dashboard then receives. Blocklists work the same way: phishing
# hosted on a user subdomain gets the whole domain flagged, and if the API lives
# there too, a report takes the platform down with it.
#
# Both records point at the same host that carries production today; splitting
# them at the DNS layer means either can move without the other.
#
# Unlike domain_name, this zone is here by choice rather than necessity: nothing
# in Yandex issues certificates for these names (Caddy on the host does that
# itself), so the zone can be hosted anywhere. Keeping it here puts every record
# in one place. Set control_plane_domain = "" to hand DNS to another provider.

locals {
  control_plane_enabled = var.control_plane_domain != "" && var.edge_ip_address != ""
}

resource "yandex_dns_zone" "control_plane" {
  count = local.control_plane_enabled ? 1 : 0

  name        = replace(var.control_plane_domain, ".", "-")
  zone        = "${var.control_plane_domain}."
  public      = true
  description = "SnapHost ${var.environment} control-plane zone (dashboard + API)"
}

# Apex serves the dashboard. An apex cannot hold a CNAME, so it is an A record.
resource "yandex_dns_recordset" "control_plane_apex" {
  count = local.control_plane_enabled ? 1 : 0

  zone_id = yandex_dns_zone.control_plane[0].id
  name    = "${var.control_plane_domain}."
  type    = "A"
  ttl     = 300
  data    = [var.edge_ip_address]
}

resource "yandex_dns_recordset" "control_plane_www" {
  count = local.control_plane_enabled ? 1 : 0

  zone_id = yandex_dns_zone.control_plane[0].id
  name    = "www.${var.control_plane_domain}."
  type    = "CNAME"
  ttl     = 300
  data    = ["${var.control_plane_domain}."]
}

resource "yandex_dns_recordset" "control_plane_api" {
  count = local.control_plane_enabled ? 1 : 0

  zone_id = yandex_dns_zone.control_plane[0].id
  name    = "${var.control_plane_api_label}.${var.control_plane_domain}."
  type    = "A"
  ttl     = 300
  data    = [var.edge_ip_address]
}

# Supabase Auth runs on its own Russian VDS, independently from the application
# control plane. Keeping this record in the existing authoritative zone avoids
# manual DNS drift while allowing the identity host to move independently.
resource "yandex_dns_recordset" "control_plane_auth" {
  count = local.control_plane_enabled && var.control_plane_auth_ip_address != "" ? 1 : 0

  zone_id = yandex_dns_zone.control_plane[0].id
  name    = "${var.control_plane_auth_label}.${var.control_plane_domain}."
  type    = "A"
  ttl     = 300
  data    = [var.control_plane_auth_ip_address]
}
