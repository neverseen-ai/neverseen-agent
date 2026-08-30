.PHONY: help build run test test-cover lint fmt tidy score score-update bench-accuracy \
	e2e-claude extension extension-e2e contract-update clean

BIN     := bin/cloakfleet
PKG     := ./cmd/cloakfleet
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

# Mirrors proxy.DefaultListen, for the URL to open. The agent logs the address it
# actually took, so a drift here shows up next to the wrong URL.
LISTEN  ?= 127.0.0.1:8787

help: ## Show this help
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "};{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

build: ## Build the agent binary into bin/
	go build -ldflags '$(LDFLAGS)' -o $(BIN) $(PKG)

run: build ## Run the agent and open its test page — loads .env if there is one
	@set -a; [ -f .env ] && . ./.env; set +a; \
	  addr=$${CLOAKFLEET_LISTEN:-$(LISTEN)}; \
	  ( sleep 1; open "http://$$addr/test" ) & \
	  exec $(BIN) proxy

test: ## Run the whole suite with the race detector
	go test -race ./...

test-cover: ## Run the suite with coverage; the CI gate is 80%
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	@go tool cover -func=coverage.out | tail -1

lint: ## Run golangci-lint
	golangci-lint run

fmt: ## Format and tidy
	gofmt -w .
	go mod tidy

tidy: fmt

score: ## Gate per-category precision/recall against the committed floor
	go test ./internal/detector/ -run TestScoreCorpus -v

score-update: ## Rewrite that floor from the current run — explain the delta in the PR
	go test ./internal/detector/ -run TestScoreCorpus -update-score -v

bench-accuracy: ## Per-category accuracy report over the corpus
	go test ./internal/detector/ -run TestAccuracyCorpus -v

e2e-claude: ## End-to-end: the Claude CLI through the agent to the real provider (spends quota)
	CLOAKFLEET_E2E_CLAUDE=1 go test ./internal/proxy/ -run TestE2EClaudeCode -v -timeout 10m

extension: ## Typecheck, test and build the browser extension into extension/dist
	cd extension && npm ci && npm run check

extension-e2e: extension ## Drive the built extension through a real Chrome against a real agent
	cd extension && npm run e2e

contract-update: ## Rewrite the recorded extension exchanges from this run — explain the delta in the PR
	go test ./internal/proxy/ -run TestExtensionContract -update-contract -v

clean: ## Remove build artefacts
	rm -rf bin dist coverage.out coverage.html
