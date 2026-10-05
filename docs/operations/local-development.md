# Local development

Status: Current
Type: Operations
Updated: 2026-10-04

## Prerequisites

- Docker 24 or newer with Compose v2;
- Go version declared in snaphost-backend/go.mod for Go-only work;
- Node.js and pnpm 11 for panel hot reload or local panel checks.

The image build includes its own Node stage, so Node is not required merely to
run the Compose stack.

## Configure

Copy the tracked example:

~~~bash
cp infra/.env.example infra/.env
~~~

No provider key is required. `LLM_ENABLED` defaults to `false`; projects use
their own Dockerfile or a built-in template. If no template matches, add a
Dockerfile to the project. Do not commit `infra/.env`.

To opt into AI generation, set both `LLM_ENABLED=true` and
`OPENROUTER_API_KEY` in `infra/.env` and recreate the application container.
An existing key alone does not enable AI. `LLM_BASE_URL` and `OPENROUTER_MODEL`
can select a compatible provider and model when the option is enabled.

To verify a complete deployment without AI credentials, build the image and
run the isolated integration test:

~~~bash
docker build -f snaphost-backend/docker/Dockerfile \
  -t snaphost-optional-ai:test snaphost-backend
python3 infra/tests/optional_ai_integration_test.py
~~~

It starts a separate application and BuildKit, checks a project Dockerfile and
a built-in template through real builds and HTTP responses, and checks that an
unsupported project fails with a Dockerfile hint without retries. It uses
temporary application state and cleans up its containers and deploy image tags.

On a Linux host, set DOCKER_SOCKET_GID to the numeric group of the Docker
socket when it is not root:

~~~bash
stat -c %g /var/run/docker.sock
~~~

The snaphost process remains a non-root UID, but Docker socket access is still
root-equivalent host authority.

## Start

~~~bash
make dev
make logs-svc SVC=snaphost
~~~

make dev starts snaphost, rootless BuildKit and Traefik. The application and
panel are available at http://localhost:8080. Traefik listens on ports 80 and
443 and exposes its development dashboard on 8090.

make dev-backend starts only snaphost and BuildKit. Generated deploy URLs then
have no local edge even though runtime creation and probing still work through
the Docker network.

BuildKit periodically garbage-collects its separate cache volume. The policy
retains at least 512 MB, starts broader reclamation above 4 GB, and tries to
leave 5 GB free on the host filesystem. Inspect current cache records with:

~~~bash
docker compose -f infra/docker-compose.yml exec -T buildkitd \
  buildctl --addr tcp://127.0.0.1:1234 du
~~~

The thresholds are not a hard quota while a build is running. `make clean`
still removes the whole development volume.

## First login

On the first start against an empty SQLite volume, the application creates the
admin account and writes a generated password once:

~~~bash
docker compose -f infra/docker-compose.yml logs snaphost
~~~

Search for the operator account log entry. The email defaults to
operator@localhost and is controlled by OPERATOR_EMAIL only during bootstrap.
The password is not stored in plaintext and cannot be read back later.

The browser receives an HttpOnly snaphost_session cookie. SESSION_COOKIE_SECURE
is derived from the incoming request when unset, which permits first login over
local HTTP. Set it to true behind production TLS.

Changing the password through POST /api/v1/auth/password revokes other
sessions. Non-browser clients should create an sk_ API key in the panel.

## Panel development

The panel is in snaphost-backend/web. With the stack running:

~~~bash
make dev-panel
~~~

Open http://localhost:5173. Vite proxies /api and /ws to 127.0.0.1:8080.

To put freshly built assets into a Go binary compiled outside Docker:

~~~bash
make build-panel
~~~

The production image does this automatically.

## Verification

~~~bash
make test
make lint
make test-panel
make lint-panel
~~~

Additional direct checks:

~~~bash
cd snaphost-backend
go build ./...
go vet ./...
gofmt -l cmd internal
~~~

The final gofmt command must print nothing. golangci-lint is pinned to v1.64.x
and is installed from source in CI so it is built by a toolchain compatible
with the Go directive.

The frontend formatting check is:

~~~bash
pnpm --dir snaphost-backend/web format:check
~~~

It is not currently a CI gate.

## Stop and reset

make stop stops containers without removing state. make clean runs Compose down
with volume removal and permanently deletes the local SQLite database and
BuildKit cache.
