# Inherited task catalog

Status: Historical. Not a description of this product.
Type: Archive

These documents belong to **SnapHost**, the multi-tenant hosting SaaS this
repository was forked from on 2026-08-29. They are kept because most of the
code they describe is still here, and they carry the reasoning behind it —
including a series of production incidents that shaped the build and runtime
paths.

Read them for *why the code looks like this*. Do not read them as a statement
of what this platform does or intends to do.

Their file links were repointed when the services moved under `internal/`, so
most still resolve. Sixteen do not, and deliberately so: they name the Yandex
runtime, `router-svc`, the VK backend stub and the cloud registry auth, none of
which exist here. A link that 404s is the correct answer to "where is this
code now".

## What is still load-bearing here

| Document | Why it still matters |
| --- | --- |
| [0014-vibecoder-ingress.md](active/0014-vibecoder-ingress.md) | Archive upload and the safe-unpack rules; private-git credential handling. |
| [0015-runtime-port-contract.md](active/0015-runtime-port-contract.md) | The `PORT` contract and the liveness probe. Both survive the fork unchanged. |
| [0016-custom-domains.md](active/0016-custom-domains.md) | The project → deploy → alias model, TXT verification, and the TLS edge. |
| [0017-admin-console.md](active/0017-admin-console.md) | The operator read surface, and why it is deliberately read-only. |
| [0013-observability-and-user-feedback.md](active/0013-observability-and-user-feedback.md) | The operator/user log split. The Yandex half is gone; the split is not. |

## What no longer applies

- Anything about vibecoins, wallets, reservations, or payment collection —
  removed, since a self-hosted platform has no one to bill.
- Anything about Yandex Cloud, Terraform, `router-svc`, staging, or the
  SnapHost production VDS — removed with the cloud runtime path.
- The launch, legal, and consent work: that was a Russian consumer SaaS
  obligation and has no counterpart here.

Current work lives in [../tasks/](../tasks/).
