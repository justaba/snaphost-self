# Local development

Status: Current
Type: Operations
Updated: 2026-08-29

## Prerequisites

- Docker 24+ with Compose v2
- Go matching `snaphost-backend/go.mod`
- Node.js compatible with the frontend lockfile
- pnpm 11

## Start

1. Clone `justaba/snaphost` and `justaba/snaphost-ui` as sibling directories.
2. Copy `infra/.env.example` to `infra/.env` and fill required values.
3. Put frontend variables in `../snaphost-ui/.env.local`.
4. Run `make dev-backend`.
5. Run `make dev-frontend` in another shell and open `http://localhost:5173`.

The convenience frontend targets use `FRONTEND_DIR=../snaphost-ui` by default.
Override it when the checkout lives elsewhere.

Backend-only startup uses `make dev-backend`. Logs use `make logs` or
`make logs-svc SVC=<compose-service>`.

Local runtime uses Docker and Traefik. It does not use `router-svc` or the
Yandex runtime backend.

## Logging in

Identity is issued by this platform. Nothing external has to be running, and
there is no credential in `infra/.env` to fill in.

On the first start against an empty database the operator account is created
and its password written to the log exactly once:

```bash
docker compose -p snaphost-self -f infra/docker-compose.yml logs snaphost \
  | grep 'operator account'
```

It is not stored in plaintext anywhere, so it cannot be read back later — copy
it, or change it with `POST /api/v1/auth/password` and use the new one. The
address defaults to `operator@localhost` and comes from `OPERATOR_EMAIL`, which
is read only while creating the account: changing it afterwards renames nothing.

```bash
curl --noproxy '*' -c cookies.txt -X POST http://127.0.0.1:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"operator@localhost","password":"<from the log>"}'
```

The response sets a `snaphost_session` cookie: HttpOnly, SameSite=Lax, and
Secure only when the request arrived over TLS — `SESSION_COOKIE_SECURE` forces
it either way. Deriving it is what lets a fresh box be logged into over plain
HTTP in order to put a certificate on it.

Sessions are rows in the `sessions` table, so `POST /api/v1/auth/logout`
actually revokes and a password change revokes every other session. They last
`SESSION_TTL_HOURS` (default 168) and slide forward while in use.

The operator is created with `role = 'admin'`, which is what opens
`/api/v1/admin/*`. There is no hook to configure and no separate database to
keep in step: the role is a column in the same file as everything else.

Non-browser clients — the MCP server, the editor extension — are unaffected.
They authenticate with an `sk_` API key in the `Authorization` header, which is
a separate path and always was.

## Verification

- `make lint` — `golangci-lint run` in every Go module
- `make test` — `go test ./...` in every Go module, including the `yandex`-tagged runner pass
- `make lint-frontend` and `make build-frontend` — checks in the sibling frontend repository

`make lint` needs **golangci-lint v1.64.x** on `PATH`; the config it reads
(`snaphost-backend/.golangci.yml`) uses the v1 schema, and v2 rejects it. Both
backend targets cover the same module list the CI `go` matrix does. Frontend
checks and deployment are owned by the standalone repository.
