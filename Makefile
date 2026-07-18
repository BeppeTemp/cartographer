.PHONY: help build test vet fmt run run-http smoke docker clean migrate e2e e2e-quick

VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)

help: ## Show this message
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*##"}; {printf "  %-12s %s\n", $$1, $$2}'

build: ## Build the binary into bin/cartographer
	go build -ldflags "-X main.version=$(VERSION)" -o bin/cartographer ./cmd/cartographer

test: ## Run all tests
	go test ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format the code with gofmt
	gofmt -w .

run: build ## Start the stdio server with a demo KB
	./bin/cartographer serve --kb ./demo-kb --init

run-http: build ## Start the HTTP server on :8080 with a demo KB
	./bin/cartographer serve --kb ./demo-kb --init --http :8080

smoke-http: build ## HTTP flow smoke test: creates KB, archives, dossiers via MCP
	@./scripts/test-kb-flow.sh

smoke: build ## Build + quick stdio test
	@echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}' | \
		./bin/cartographer serve --kb ./demo-kb --init 2>/dev/null | \
		grep -q '"protocolVersion"' && echo "smoke: OK" || (echo "smoke: FAIL" && exit 1)

docker: ## Build the Docker image
	docker build -t cartographer .

clean: ## Remove bin/ and demo-kb/
	rm -rf bin/ demo-kb/

migrate: ## Migrate an existing wiki (SRC=<wiki-dir> DST=<kb-dir>)
	@./scripts/migrate-wiki.sh $(SRC) $(DST)

e2e: build ## Run all agent-level E2E scenarios with headless OpenCode
	@./test/e2e/run.sh

e2e-quick: build ## Run only the basic CRUD scenario (01_mcp_crud)
	@./test/e2e/run.sh --only 01_mcp_crud
