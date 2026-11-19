SHELL := /bin/bash
.DEFAULT_GOAL := help

BINARY        := bin/api
PKG           := ./...
LINT_VERSION  := v2.15.0
LINT          := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(LINT_VERSION)

.PHONY: help
help: ## Show available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build the API binary into ./bin
	CGO_ENABLED=0 go build -trimpath -o $(BINARY) ./cmd/api

.PHONY: run
run: ## Run the API locally
	go run ./cmd/api

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

.PHONY: lint
lint: ## Run golangci-lint (pinned version)
	$(LINT) run

.PHONY: tidy
tidy: ## Tidy go.mod/go.sum
	go mod tidy

.PHONY: check
check: fmt-check vet lint test ## Everything CI runs (minus integration)

# TODO(slice-3): migrate, compose-up, compose-down targets.
# TODO(slice-10): docker-build target.
