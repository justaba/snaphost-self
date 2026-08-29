resource "yandex_dns_zone" "main" {
  name        = replace(var.domain_name, ".", "-")
  zone        = "${var.domain_name}."
  public      = true
  description = "SnapHost ${var.environment} DNS zone"
}

# The reserved public address customers point DNS at (Task 16 P2).
#
# Published as a hostname rather than a bare IP so the edge can be rebuilt or
# moved without every customer having to edit their own zone. The wildcard
# CNAME above serves our own subdomains through API Gateway; this record serves
# foreign hostnames, whose TLS the gateway's wildcard certificate cannot cover.
#
# A short TTL is deliberate: it is the only lever we have if the address ever
# has to change, and it costs nothing at this traffic volume.
resource "yandex_dns_recordset" "edge" {
  count = var.edge_ip_address == "" ? 0 : 1

  zone_id = yandex_dns_zone.main.id
  name    = "${var.edge_hostname_label}.${var.domain_name}."
  type    = "A"
  ttl     = 300
  data    = [var.edge_ip_address]
}

resource "yandex_dns_recordset" "wildcard_gateway" {
  zone_id = yandex_dns_zone.main.id
  name    = "*.${var.domain_name}."
  type    = "CNAME"
  ttl     = 300
  data    = [yandex_api_gateway.router.domain]
}
