# Backlog

Status: Current
Updated: 2026-08-03

| Item | Reason / entry condition |
| --- | --- |
| Task 5b — transient Redis retries | Add reclaim, bounded retry, and dead-letter behavior when transient failures justify it. |
| Complete Task 10.7 proof | Automate the full builder push, scan, validation, Yandex pull, and public route scenario. |
| Yandex Cloud Logging | Implemented but blocked on a Yandex log-group permission; see [Task 13a](active/0013-observability-and-user-feedback.md). |
| Yandex vulnerable-image deletion | Implement Yandex Registry cleanup behind the registry client boundary. |
| Router route cache | Add only with explicit invalidation/staleness semantics. |
| Router protocol features | Authorization forwarding, WebSocket, large uploads, and long streaming. |
| Provider abstraction audit | Required before implementing a second production provider. |
| Builder egress hardening | Block metadata address ranges at the container/network layer. |
| Python template version selection | Consume the already detected `.python-version` signal. |
| Remote Terraform state | Required before multiple operators manage production infrastructure. |
| Public Suffix List entry for `snaphost.pw` | Submit and see through review so browsers isolate one deploy's cookies from another's. Weeks to months, and can be refused. `router-svc` strips wide `Set-Cookie` domains meanwhile, which does not cover cookies set by page JavaScript. Requires >2 years left on the registration and a `_psl` TXT record naming the PR. |
| Terraform state access logging | Enable and verify Object Storage access logging for both environment state buckets before any production apply. The first staging apply has an explicitly approved exception; private access, versioning, KMS encryption, and environment isolation remain mandatory. |

Credential separation is now scoped as
[Task 12](active/0012-credential-separation.md) rather than a backlog line.

To start an item, create a scoped document under `active/`, define acceptance
criteria, and link it from [README.md](README.md).
