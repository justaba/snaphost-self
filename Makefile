.PHONY: dev dev-frontend dev-backend build build-frontend lint lint-frontend test stop clean logs logs-svc

FRONTEND_DIR ?= ../snaphost-ui

# Starts the local backend stack via docker compose
dev:
	docker compose -f infra/docker-compose.yml up -d

# Starts only the frontend dev server
dev-frontend:
	pnpm --dir "$(FRONTEND_DIR)" dev

# Starts only backend services via docker compose
dev-backend:
	docker compose -f infra/docker-compose.yml up -d \
		postgres redis \
		api-gateway \
		user-billing \
		buildkitd registry \
		builder-api builder-worker \
		runner-api runner-watchdog \
		ai-orchestrator \
		traefik

# Builds all Docker images via docker compose build
build:
	docker compose -f infra/docker-compose.yml build

# Builds frontend production bundle
build-frontend:
	pnpm --dir "$(FRONTEND_DIR)" build

# snaphost-backend/ is one Go module (Task 1 item 4), so both targets below are
# a single ./... walk. There used to be a GO_MODULES list here that had to be
# kept in step by hand with the CI matrix; a module missing from either was
# silently never checked, which is the class of gap this removes.
GO_MODULE_DIR := snaphost-backend

# Configuration is snaphost-backend/.golangci.yml. Requires golangci-lint v1.64.x.
lint:
	@cd $(GO_MODULE_DIR) && golangci-lint run ./...

# Convenience target for the standalone sibling frontend checkout.
lint-frontend:
	pnpm --dir "$(FRONTEND_DIR)" lint

test:
	@cd $(GO_MODULE_DIR) && go test ./...

# Stops all docker compose services
stop:
	docker compose -f infra/docker-compose.yml stop

# Stops and removes all containers, volumes, networks
clean:
	docker compose -f infra/docker-compose.yml down -v --remove-orphans

# Tails logs from all docker compose services
logs:
	docker compose -f infra/docker-compose.yml logs -f

# Tails logs for a specific service: make logs-svc SVC=api-gateway
logs-svc:
	docker compose -f infra/docker-compose.yml logs -f $(SVC)
