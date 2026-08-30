.PHONY: dev dev-backend dev-panel build build-panel lint lint-panel test test-panel stop clean logs logs-svc

COMPOSE := docker compose -f infra/docker-compose.yml

# The panel lives in this repository now, at snaphost-backend/web, and is
# compiled into the binary from snaphost-backend/internal/panel/dist. It used
# to be a sibling checkout reached through a FRONTEND_DIR variable; a
# self-hosted platform that ships as one binary cannot ask its operator to
# clone two repositories and host the second one.
PANEL_DIR := snaphost-backend/web

# Starts the local stack via docker compose. The project name is set in the
# compose file itself, not passed here, so every invocation agrees on it.
dev:
	$(COMPOSE) up -d

# Starts the stack without Traefik, for working on the API alone.
dev-backend:
	$(COMPOSE) up -d buildkitd snaphost

# Runs the panel's Vite dev server against a stack started by `make dev`.
# vite.config.ts proxies /api and /ws to 127.0.0.1:8080, so the panel reloads
# on save without rebuilding the image.
dev-panel:
	pnpm --dir "$(PANEL_DIR)" dev

# Builds the image. The panel is built inside it, in its own stage, so this
# needs no local Node.
build:
	$(COMPOSE) build

# Builds the panel into snaphost-backend/internal/panel/dist, which is where
# //go:embed reads it. Only needed to get a panel into a binary built with
# plain `go build` — the image build does this itself.
build-panel:
	pnpm --dir "$(PANEL_DIR)" build

# snaphost-backend/ is one Go module (Task 1 item 4), so both targets below are
# a single ./... walk. There used to be a GO_MODULES list here that had to be
# kept in step by hand with the CI matrix; a module missing from either was
# silently never checked, which is the class of gap this removes.
GO_MODULE_DIR := snaphost-backend

# Configuration is snaphost-backend/.golangci.yml. Requires golangci-lint v1.64.x.
lint:
	@cd $(GO_MODULE_DIR) && golangci-lint run ./...

lint-panel:
	pnpm --dir "$(PANEL_DIR)" lint

test:
	@cd $(GO_MODULE_DIR) && go test ./...

test-panel:
	pnpm --dir "$(PANEL_DIR)" test

# Stops all docker compose services
stop:
	$(COMPOSE) stop

# Stops and removes all containers, volumes, networks
clean:
	$(COMPOSE) down -v --remove-orphans

# Tails logs from all docker compose services
logs:
	$(COMPOSE) logs -f

# Tails logs for a specific service: make logs-svc SVC=snaphost
logs-svc:
	$(COMPOSE) logs -f $(SVC)
