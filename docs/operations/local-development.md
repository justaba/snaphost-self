# Local development

Status: Current
Type: Operations
Updated: 2026-08-12

## Prerequisites

- Docker 24+ with Compose v2
- Go matching each module's `go.mod`
- Node.js compatible with the frontend lockfile
- pnpm 11

## Start

1. Clone `justaba/snaphost`, `justaba/snaphost-ui`, and
   `justaba/snaphost-supabase` as sibling directories.
2. Run `make -C ../snaphost-supabase supabase-start`.
3. Copy `infra/.env.example` to `infra/.env` and fill required values.
4. Put frontend variables in `../snaphost-ui/.env.local`.
5. Run `make dev-backend`.
6. Run `make dev-frontend` in another shell and open `http://localhost:5173`.

The convenience frontend targets use `FRONTEND_DIR=../snaphost-ui` by default.
Override it when the checkout lives elsewhere.

Backend-only startup uses `make dev-backend`. Logs use `make logs` or
`make logs-svc SVC=<compose-service>`.

Local runtime uses Docker and Traefik. It does not use `router-svc` or the
Yandex runtime backend.

## Local Supabase

Authentication is Supabase even locally. Its CLI project is owned by the sibling
`justaba/snaphost-supabase` checkout and must be running before the dashboard can
log in:

```bash
make -C ../snaphost-supabase supabase-start
make -C ../snaphost-supabase supabase-status
```

**The CLI version is pinned** (`SUPABASE_CLI_VERSION` in the standalone
repository's Makefile) and installed into its gitignored `.tools/`. This is not
tidiness. A CLI release
hard-codes the image tags it starts, so `npx supabase` — which resolves to
whatever is latest today — can turn a routine restart into a multi-gigabyte pull
of a different image set. That is exactly what happened on 2026-08-07: the stack
had been started months earlier by an older CLI, `npx` fetched the current one,
and it wanted eight different images. Move the pin deliberately, expecting the
download, rather than discovering it mid-restart.

`supabase stop` keeps the database and restores it on the next start. It does
**not** keep the containers, which matters for the next two points.

### SUPABASE_URL must be `http://host.docker.internal:54321`

api-gateway prefetches JWKS at startup and exits fatally if that host is
unreachable, so a wrong value here is a crash loop, not a degraded feature.

The tempting value is the kong container name
(`http://supabase_kong_<project>:8000`), and it works — right up until the next
`supabase stop`. The CLI puts kong on its own Docker network, so reaching it
from a compose service needs `docker network connect snaphost-net
supabase_kong_<project>`, and that wiring dies with the container. The published
port does not.

### Keep the local database in step with the standalone migrations

Nothing applies those migrations automatically — unlike the control-plane
migrations, which user-billing runs at startup. A local project can therefore
sit for months on a schema the repository has since moved past, and the
divergence surfaces as behaviour, not as an error at startup.

Re-apply after pulling identity-schema changes in `snaphost-supabase`:

```bash
docker exec -i supabase_db_<project> psql -U postgres -d postgres \
  -v ON_ERROR_STOP=1 \
  < ../snaphost-supabase/supabase/migrations/0001_profiles_and_hooks.sql
```

`0001` is idempotent and carries no placeholders, so it is safe to re-run.
**`0002` is not** — it has `__API_GATEWAY_URL__` and `__WEBHOOK_SECRET__` that
must be substituted per environment, and re-running it unsubstituted replaces a
working webhook trigger with a broken one.

What an audit on 2026-08-07 found in a local project that had drifted:

- `custom_access_token_hook` existed but **without `security definer`**, and the
  RLS policy letting `supabase_auth_admin` read `profiles` was missing. Login
  failed with `Error running hook URI` — the exact symptom the migration's own
  comment predicts;
- `profile_role` and `is_admin` did not exist, and the admin policies used an
  inline `EXISTS (SELECT … FROM profiles)` instead. A policy on `profiles` that
  itself reads `profiles` is the recursion the security-definer functions were
  introduced to break;
- a hand-made `user_created_to_snaphost` trigger sat beside the migration's
  `profiles_seed_wallet`, POSTing every local signup to the **production**
  gateway with a placeholder secret. Harmless to production because it fails
  authentication, but it sends local test emails across the internet.

The same audit is worth running against the production project, where nothing
re-applies these migrations either.

### Turning an account into an admin

The `snaphost_role` claim comes from `public.profiles.role`, injected by the
access-token hook. The hook must be enabled — locally by
`[auth.hook.custom_access_token]` in
`../snaphost-supabase/supabase/config.toml`, in a hosted project through
Authentication → Hooks. Without it every token comes back with no claim
and api-gateway falls back to `user` for everyone, including an account whose
row already says `admin`.

```bash
docker exec supabase_db_<project> psql -U postgres -d postgres \
  -c "update public.profiles set role='admin' where email='you@example.com'"
```

Then **log in again** — the claim is written when a token is issued, so an open
session keeps its old role.

## Verification

- `make lint` — `golangci-lint run` in every Go module
- `make test` — `go test ./...` in every Go module, including the `yandex`-tagged runner pass
- `make lint-frontend` and `make build-frontend` — checks in the sibling frontend repository

`make lint` needs **golangci-lint v1.64.x** on `PATH`; the config it reads
(`snaphost-backend/.golangci.yml`) uses the v1 schema, and v2 rejects it. Both
backend targets cover the same module list the CI `go` matrix does. Frontend
checks and deployment are owned by the standalone repository.
