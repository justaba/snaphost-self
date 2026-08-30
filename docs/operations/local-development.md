# Local development

Status: Current
Type: Operations
Updated: 2026-08-30

## Prerequisites

- Docker 24 or newer with Compose v2;
- Go version declared in snaphost-backend/go.mod for Go-only work;
- Node.js and pnpm 11 for panel hot reload or local panel checks.

The image build includes its own Node stage, so Node is not required merely to
run the Compose stack.

## Configure

Copy the tracked example and replace both required placeholder secrets:

~~~bash
cp infra/.env.example infra/.env
~~~

WEBHOOK_SECRET authenticates internal routes and must be a long random value.
OPENROUTER_API_KEY is currently required at startup even when a built-in
Dockerfile template would satisfy a particular deploy. Do not commit
infra/.env.

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
