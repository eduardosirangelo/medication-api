SHELL := /bin/bash
.DEFAULT_GOAL := help

BINARY        := bin/api
PKG           := ./...
LINT_VERSION  := v2.15.0
LINT          := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(LINT_VERSION)
VULN_VERSION  := v1.10.0
VULN          := go run golang.org/x/vuln/cmd/govulncheck@$(VULN_VERSION)
REDOCLY_VERSION := 2.82.0
IMAGE         := medication-api:local

# Load .env (if present) into the environment of recipes that run the app.
ifneq (,$(wildcard .env))
include .env
export
endif

# Host ports of the Docker stack. Change them, with COMPOSE_PROJECT_NAME, to run
# a second checkout alongside this one (see compose.yaml).
API_PORT ?= 8080
DB_PORT  ?= 5432
export API_PORT DB_PORT

.PHONY: help
help: ## Show available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: env-check
env-check:
	@test -f .env || { echo "error: .env not found. Copy .env.example to .env (cp .env.example .env) and adjust it."; exit 1; }

# Builds once and runs the binary, instead of three `go run` compiles. Ctrl+C
# reaches the API (graceful shutdown); the database container keeps running.
.PHONY: dev
dev: env-check ## One command for local dev: db-up, migrate, seed, then run the API in the foreground
	@$(MAKE) --no-print-directory db-up build
	./$(BINARY) migrate
	./$(BINARY) seed
	./$(BINARY)

# Needs only Docker: images are built in the container, no local Go or .env.
# The port check matters: a local API (make dev) listening on [::]:8080 does
# not stop Docker binding 127.0.0.1:8080, and `localhost` then reaches the
# local process over IPv6 instead of the container, silently. Only this
# project's own api container may already answer there: `docker compose ps` is
# scoped to the compose project, so another checkout's stack counts as taken.
.PHONY: up
up: ## Run the full stack in Docker (db, migrate, seed, api on API_PORT) and wait until healthy
	@if curl -s -o /dev/null --max-time 1 http://localhost:$(API_PORT)/healthz && [ -z "$$(docker compose ps -q api)" ]; then \
		echo "error: something already serves localhost:$(API_PORT) (make dev, make run, another checkout?). Stop it or set API_PORT."; exit 1; fi
	docker compose up -d --build --wait
	@echo "API ready on http://localhost:$(API_PORT) (key: local-dev-key-change-me). Try: make smoke"

.PHONY: down
down: ## Stop and remove the Docker stack (keeps the data volume)
	docker compose down

.PHONY: logs
logs: ## Follow the API container logs
	docker compose logs -f api

.PHONY: docker-build
docker-build: ## Build the runtime image (medication-api:local)
	docker build -t $(IMAGE) .

.PHONY: stop
stop: ## Stop the local containers (keeps the data volume; `make db-up` resumes)
	docker compose stop

.PHONY: build
build: ## Build the API binary into ./bin
	CGO_ENABLED=0 go build -trimpath -o $(BINARY) ./cmd/api

.PHONY: run
run: env-check ## Run the API locally (reads .env; needs `make db-up`)
	go run ./cmd/api

.PHONY: migrate
migrate: env-check ## Apply database migrations (reads .env)
	go run ./cmd/api migrate

.PHONY: seed
seed: env-check ## Insert the sample catalog; idempotent, never run against production
	go run ./cmd/api seed

.PHONY: smoke
smoke: ## Run the end-to-end smoke test against a running API (BASE_URL, API_KEY)
	BASE_URL=$${BASE_URL:-http://localhost:$(API_PORT)} ./scripts/smoke.sh

.PHONY: db-up
db-up: ## Start local PostgreSQL and wait until healthy
	docker compose up -d --wait db

.PHONY: db-down
db-down: ## Remove the local containers (keeps data; same as `make down`)
	docker compose down

.PHONY: db-reset
db-reset: ## Remove the local containers and delete the data volume
	docker compose down -v

.PHONY: db-psql
db-psql: ## Open psql against the local database
	docker compose exec db psql -U medication -d medication

.PHONY: fmt
fmt: ## Format code
	gofmt -s -w .

.PHONY: fmt-check
fmt-check: ## Fail if any file is not gofmt-ed
	@out=$$(gofmt -s -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## Run go vet
	go vet $(PKG)

.PHONY: test
test: ## Run unit tests with the race detector
	go test -race -count=1 $(PKG)

.PHONY: test-integration
test-integration: ## Run integration tests (requires Docker)
	go test -race -count=1 -tags=integration $(PKG)

.PHONY: bench
bench: ## Run benchmarks (no tests) with allocation counts
	go test -run='^$$' -bench=. -benchmem $(PKG)

FUZZTIME ?= 30s
.PHONY: fuzz
fuzz: ## Fuzz the decimal parser for FUZZTIME (default 30s)
	go test -run='^$$' -fuzz=FuzzAmountRoundTrip -fuzztime=$(FUZZTIME) ./internal/medication

.PHONY: lint
lint: ## Run golangci-lint (pinned version)
	$(LINT) run

.PHONY: vuln
vuln: ## Scan dependencies and stdlib for known vulnerabilities (govulncheck, pinned)
	$(VULN) ./...

.PHONY: openapi-lint
openapi-lint: ## Lint openapi.yaml with Redocly CLI (pinned; needs Node.js, not part of check/CI)
	npx --yes @redocly/cli@$(REDOCLY_VERSION) lint openapi.yaml

.PHONY: tidy
tidy: ## Tidy go.mod/go.sum
	go mod tidy

.PHONY: check
check: fmt-check vet lint vuln test ## Everything CI runs (minus integration)

# The stack is taken down even when the smoke test fails, and the smoke
# test's exit status is kept.
.PHONY: verify
verify: ## Full pre-commit gate: check, integration, short fuzz, one-pass benchmarks, build, OpenAPI lint, Docker stack + smoke
	$(MAKE) check
	go vet -tags=integration $(PKG)
	$(MAKE) test-integration
	$(MAKE) fuzz FUZZTIME=10s
	go test -run='^$$' -bench=. -benchtime=1x $(PKG)
	$(MAKE) build
	$(MAKE) openapi-lint
	$(MAKE) up
	$(MAKE) smoke; status=$$?; $(MAKE) down; exit $$status
