.PHONY: help build test vet fmt run run-http smoke smoke-http docker clean e2e

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
	@./test/smoke/http.sh

smoke: build ## Build + quick stdio test
	@echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}' | \
		./bin/cartographer serve --kb ./demo-kb --init 2>/dev/null | \
		grep -q '"protocolVersion"' && echo "smoke: OK" || (echo "smoke: FAIL" && exit 1)

docker: ## Build the Docker image
	docker build -t cartographer .

clean: ## Remove bin/ and demo-kb/
	rm -rf bin/ demo-kb/

e2e: ## Run deterministic HTTP/CLI end-to-end scenarios
	@./test/e2e/run.sh
