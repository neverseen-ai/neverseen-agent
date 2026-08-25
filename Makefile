.PHONY: help build test test-cover lint fmt tidy score score-update bench-accuracy e2e-claude clean

BIN     := bin/cloakfleet
PKG     := ./cmd/cloakfleet
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

help: ## Show this help
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "};{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

build: ## Build the agent binary into bin/
	go build -ldflags '$(LDFLAGS)' -o $(BIN) $(PKG)

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

clean: ## Remove build artefacts
	rm -rf bin dist coverage.out coverage.html
