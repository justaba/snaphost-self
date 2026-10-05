# Optional AI Dockerfile generation — 2026-10-04

Status: **passed locally**

AI generation is an explicit opt-in: `LLM_ENABLED` defaults to `false` even
when a legacy API key remains configured. Only `LLM_ENABLED=true` requires
`OPENROUTER_API_KEY`. Existing project Dockerfiles and built-in templates work
without AI; an unsupported project fails permanently with a Dockerfile hint.
Disabled AI also skips use of previously cached provider responses.

## Automated checks

Go checks ran in `golang:1.25-bookworm`, with the module mounted at `/app`:

```bash
go test ./internal/ai/... ./internal/builder/pipeline/...
go build ./...
go vet ./...
go test ./...
go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8
golangci-lint run ./...
```

All passed. Configuration tests cover default opt-out, an existing key without
opt-in, explicit enablement, missing/blank keys and an invalid switch. A real
HTTP client against a local test provider verifies zero requests when AI is
disabled, template generation with either setting, cache behavior, and a
successful generation request after opt-in. The builder classifies disabled
AI as permanent, preserving the operator hint.

The shell suites ran under `ubuntu:24.04`:

```bash
bash infra/tests/snaphostctl_test.sh
bash infra/tests/deploy_test.sh
bash infra/tests/backup_test.sh
```

Results: **31 / 62 / 41 passed**, zero failures. They cover an unattended
installation without a key, explicit enablement with a protected key file,
refusal of missing keys or invalid switches, and no key in command logs.
Shell syntax checks and `shellcheck --severity=warning` also passed.

Both Compose manifests render with `--env-file /dev/null` and no provider
credentials: the application receives `LLM_ENABLED=false`. Production uses
only the three required values: version, control hostname and ACME email.
Caddy receives no provider key or Docker socket.

## Real container and deployment checks

```bash
docker build -f snaphost-backend/docker/Dockerfile \
  -t snaphost-optional-ai:test snaphost-backend
python3 infra/tests/optional_ai_integration_test.py
```

The test used a separate application container and rootless BuildKit,
temporary application state, ephemeral loopback ports, and no API key or
explicit AI opt-in. A deliberately unreachable `LLM_BASE_URL` ensured the
test deployments could not depend on a live provider.

| Check | Result |
| --- | --- |
| Application start without key | `/health` HTTP 200 |
| Archive containing only `index.html` | nginx template built; running site HTTP 200 with expected content |
| Archive containing a project Dockerfile | supplied Dockerfile built; running site HTTP 200 with expected content |
| Archive containing only `README.md` | failed with `add a Dockerfile` hint |
| Unsupported-project retry count | 0 |
| Provider usage recorded across the three deploys | 0 |
| Explicit `LLM_ENABLED=true` without key | process refused startup with the configuration error |

The integration test removed its containers, networks and generated deploy
image tags. The initial authored test fixture used a BusyBox HTTP applet that
was absent from its Alpine image; the final fixture uses nginx with an
explicit configuration and passed. No application workaround was needed.

## Edge regression check

```bash
bash infra/tests/caddy_integration_test.sh
```

Passed against the pinned stock Caddy 2.10.2 image: control/project HTTPS,
alias B/A, detach 404, denied SNI and the same certificate after a restart.
The production Caddyfile also passed `caddy validate` with that image. This
test uses Caddy's internal CA; it does not constitute public ACME acceptance
or change Task 4's **In progress** status.
