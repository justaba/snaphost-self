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

# Every Go module under snaphost-backend/. They are separate modules, so each
# loop below enters the directory rather than relying on one ./... walk.
GO_MODULES := api-gateway user-billing builder-svc runner-svc ai-orchestrator shared

# Runs golangci-lint for each backend module.
# Configuration is snaphost-backend/.golangci.yml, found by walking up from
# each module directory. Requires golangci-lint v1.64.x.
lint:
	@for m in $(GO_MODULES); do \
		echo "==> lint $$m"; \
		(cd snaphost-backend/$$m && golangci-lint run) || exit 1; \
	done

# Convenience target for the standalone sibling frontend checkout.
lint-frontend:
	pnpm --dir "$(FRONTEND_DIR)" lint

# Runs go test ./... for each backend module
test:
	@for m in $(GO_MODULES); do \
		echo "==> test $$m"; \
		(cd snaphost-backend/$$m && go test ./...) || exit 1; \
	done

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
