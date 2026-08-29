# Public address contract

Status: Current
Type: Operations
Updated: 2026-07-30

This is the address customers point their own DNS at. It is the one value in
the platform that other people's zones depend on, so it is documented
separately from the host that currently answers on it.

| Value | Setting | Used by |
| --- | --- | --- |
| `edge.snaphost.pw` | `DOMAIN_CNAME_TARGET` | customers with a subdomain (`www`, `app`) |
| `135.106.166.76` | `DOMAIN_A_RECORD_TARGET` | customers with an apex domain (`example.com`) |

The edge lives on `snaphost.pw`, the same registrable domain as user deploys,
and not on the control-plane domain — it serves user content, so it belongs
where user content's cookies and blocklist reputation already are (see
[domain split](../architecture/deployment-model.md)). The dashboard and API
answer on `snaphost.ru`, which resolves to the same host but must never be
offered as a DNS target to customers.

`snaphost.online` served deploys until 2026-08-03 and is now parked as a spare.
It is listed in `RESERVED_DOMAINS` so nobody can attach it, and it is not a
valid DNS target for anyone.

Both are surfaced to users by `POST /api/v1/domains`, which refuses with
`edge_address_unreserved` while they are empty. The A record itself is managed
in Terraform ([dns.tf](../../terraform/yandex/dns.tf),
`yandex_dns_recordset.edge`) from `edge_ip_address` in `terraform.tfvars`.

## Why a hostname and not just the IP

Customers who can use a `CNAME` point at `edge.snaphost.pw`, so the
platform can move to a different host by editing one A record in our own zone.
An apex domain frequently cannot hold a `CNAME`, so those customers point an
`A` record at the address directly — for them, the IP *is* the contract.

That asymmetry is the whole reason this document exists: the hostname is cheap
to change, the address is not.

## Rules

- **Do not change `edge_ip_address` without a migration plan.** Every
  apex-domain customer's site goes dark the moment the old address stops
  answering, silently, and only they can fix it at their own registrar.
- **The address must survive a host rebuild.** It is a static address on the
  provider side; if the host is reinstalled or replaced, the address must be
  re-attached to the new machine rather than re-issued. A rebuild that changes
  it is an outage for every custom domain, not a maintenance window.
- **Do not publish an address before it is reserved.** The attach API is
  designed to refuse rather than hand out a target that might move.

## Current host

`135.106.166.76` is a static address on a rented server that replaces the
temporary staging VDS and now carries production. Ports observed on 2026-08-04:
`22`, `80`, and `443` open; `5432`, `6379`, and `1234` closed.

`80`/`443` answer because Caddy terminates TLS there for the control plane
(`snaphost.ru`, `www`, `api`). **That is not the custom-domain edge.** Nothing
yet answers for a hostname a customer points at this address: Caddy serves the
names it holds certificates for, and a foreign hostname is not among them.
Task 16c is what makes that address serve an attached domain.

So today a customer can attach a domain and prove ownership over DNS (the TXT
challenge resolves), but traffic to that hostname will not be served. Do not
invite external users to attach domains until 16c ships.

## If the address ever has to change

1. Stand up the new address and confirm it serves every currently `verified`
   domain (`select domain from custom_domains where status = 'verified'`).
2. Update `edge.snaphost.pw` first — that migrates every CNAME customer
   with no action on their side, within the record's 300-second TTL.
3. Keep the old address answering for apex customers while they migrate. There
   is no way to move them without their cooperation; treat the old address as
   load-bearing until their zones are checked, one by one.
4. Only then release the old address, and update this document plus
   `DOMAIN_A_RECORD_TARGET` in the production env.

## Verification

```bash
dig +short A edge.snaphost.pw           # must return the address in the table
dig +short TXT _snaphost-verify.<customer-domain>
```

Related: [Task 16](../tasks/active/0016-custom-domains.md) P2 and 16c,
[staging deployment](staging-deployment.md),
[production deployment](production-deployment.md).
