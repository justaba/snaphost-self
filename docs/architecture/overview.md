# Architecture overview

Status: Current
Type: Architecture
Updated: 2026-07-03

SnapHost builds a public Git repository into a container image and runs it on a
selected runtime backend. Docker is the local development backend. Yandex
Serverless Containers are the first production runtime adapter.

```text
Browser
  |
  v
API Gateway ---> user-billing / deploy saga
                     |             |
                     v             v
                builder-svc    runner-svc
                     |             |
                     v             v
                BuildKit +      Docker or Yandex
                Registry        Serverless Containers
                                      |
                                      v
                              router-svc + API Gateway
```

Redis carries build jobs, build events, and deploy lifecycle logs. PostgreSQL
stores users, wallets, deploy records, saga state, transactions, and AI cache
data. Internal HTTP calls use `X-Webhook-Secret`; public calls use Supabase JWT
authentication through the API gateway.

The provider boundary is deliberate: core billing, build, and saga logic must
not depend directly on Yandex APIs. Provider-specific behavior lives behind
runner backend and registry boundaries.

See [services.md](services.md), [deploy-lifecycle.md](deploy-lifecycle.md), and
[security.md](security.md) for details.
