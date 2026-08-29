locals {
  certificate_domains         = ["*.${var.domain_name}", var.domain_name]
  certificate_challenge_count = 1
}

resource "yandex_cm_certificate" "wildcard" {
  name    = var.certificate_name
  domains = local.certificate_domains

  managed {
    challenge_type = "DNS_CNAME"
  }

  # A certificate cannot be deleted while anything still references it, and the
  # API Gateway references this one until it is rebound. Destroy-then-create
  # therefore deadlocks on any change to the domain list: the delete fails
  # because the gateway holds it, and the gateway cannot be rebound because the
  # replacement does not exist yet. Creating the new certificate first breaks
  # that cycle, and it is the right order for renewals too — the old one keeps
  # serving until the new one is bound.
  lifecycle {
    create_before_destroy = true
  }
}

# Auto-create the validation CNAME records that Certificate Manager asks for.
# Without these, the cert sits in "Validating" forever.
resource "yandex_dns_recordset" "cert_validation" {
  count   = local.certificate_challenge_count
  zone_id = yandex_dns_zone.main.id
  name    = yandex_cm_certificate.wildcard.challenges[count.index].dns_name
  type    = yandex_cm_certificate.wildcard.challenges[count.index].dns_type
  ttl     = 600
  data    = [yandex_cm_certificate.wildcard.challenges[count.index].dns_value]
}
