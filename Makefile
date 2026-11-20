SHELL := /bin/bash
.DEFAULT_GOAL := help

BINARY        := bin/api
PKG           := ./...
LINT_VERSION  := v2.15.0
LINT          := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(LINT_VERSION)

# Load .env (if present) into the environment of recipes that run the app.
ifneq (,$(wildcard .env))
include .env
export
endif

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
	./scripts/smoke.sh

.PHONY: db-up
db-up: ## Start local PostgreSQL and wait until healthy
	docker compose up -d --wait db

.PHONY: db-down
db-down: ## Stop local PostgreSQL (keeps data)
	docker compose down

.PHONY: db-reset
db-reset: ## Stop local PostgreSQL and delete its data volume
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

.PHONY: tidy
tidy: ## Tidy go.mod/go.sum
	go mod tidy

.PHONY: check
check: fmt-check vet lint test ## Everything CI runs (minus integration)

# TODO(slice-10): docker-build target.
