# ADR 0005 — Isolated Terraform state backends

Status: Accepted
Date: 2026-07-05

## Context

Terraform state contains infrastructure identifiers and generated service
account private keys. A shared bucket or shared backend identity would allow an
environment-selection mistake to expose or modify another environment's state.

Yandex Object Storage accepts the AWS S3 backend protocol, but the tested
`aws:SecureTransport` deny policy is not behaviorally compatible: it blocked
valid HTTPS backend requests. Keeping that policy would make the backend
unavailable.

## Decision

Each provider and environment owns a separate state bucket and backend
identity. Staging and production identities must not have access to each
other's buckets. Future AWS and Google Cloud roots use their own provider-native
state backends rather than sharing the Yandex buckets.

For Yandex state, TLS is enforced by the versioned backend contract:

```hcl
endpoints = {
  s3 = "https://storage.yandexcloud.net"
}
```

The endpoint is fixed in Terraform code, not supplied by an environment
backend file. Standard certificate verification remains enabled. HTTP endpoint
support and TLS-verification bypasses are forbidden. Any endpoint change
requires security review.

Do not apply an AWS `aws:SecureTransport` bucket policy to Yandex Object
Storage unless Yandex documents and a staging test proves compatible behavior.

## Consequences

- A compromised or misconfigured staging backend identity cannot read or write
  production state.
- Terraform's supported Yandex workflow always transmits state and credentials
  over the fixed HTTPS endpoint.
- Bucket policy does not provide a second independent HTTP-denial layer; this
  is an accepted Yandex-specific limitation.
- Access logging remains a separate hardening control and does not redefine
  the accepted TLS boundary.
- Adding another cloud requires a separate backend decision/configuration, not
  a change to this Yandex endpoint.
