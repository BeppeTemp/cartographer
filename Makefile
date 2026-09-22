.PHONY: help build test vet fmt fmt-check run run-http smoke smoke-http docker clean e2e test-install worktree-add worktree-rm gate decisions-index decisions-next decisions-new codemap

VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)

# gofmt takes paths, and `gofmt -l .` walks every hidden directory — including
# .worktrees/, where a sibling plan's subagent is working. That made `make gate`
# fail on someone else's unfinished code and `make fmt` rewrite it, which is
# exactly what the implement-issue mandate forbids. Naming the two directories
# that hold this module's Go code scopes both targets correctly, and unlike
# `git ls-files` it still sees a file that has been written but not yet staged —
# a gate that passes locally and fails in CI is worse than no gate (D209).
GO_DIRS = cmd internal

help: ## Show this message
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*##"}; {printf "  %-12s %s\n", $$1, $$2}'

build: ## Build the binary into bin/cartographer
	go build -ldflags "-X main.version=$(VERSION)" -o bin/cartographer ./cmd/cartographer

test: ## Run all tests
	go test ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format the code with gofmt
	@gofmt -w $(GO_DIRS)

fmt-check: ## Fail if anything is not gofmt-clean (part of the gate)
	@out=$$(gofmt -l $(GO_DIRS)); \
	if [ -n "$$out" ]; then \
		echo "gofmt: these files are not formatted — run 'make fmt':"; \
		echo "$$out"; \
		exit 1; \
	fi
	@echo "gofmt: clean"

run: build ## Start the stdio server with a demo KB
	./bin/cartographer serve --kb ./demo-kb --init

run-http: build ## Start the HTTP server on :39273 with a demo KB
	./bin/cartographer serve --kb ./demo-kb --init --http :39273

smoke-http: build ## HTTP flow smoke test: creates KB, archives, dossiers via MCP
	@./test/smoke/http.sh

smoke: build ## Build + quick stdio test
	@# stdin is held open past the request: in MCP stdio the client owns the
	@# pipe's lifetime, and the server tears the session down on EOF. Closing
	@# it immediately (a bare `echo |`) races the response out of existence.
	@{ echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'; sleep 1; } | \
		./bin/cartographer serve --kb ./demo-kb --init 2>/dev/null | \
		grep -q '"protocolVersion"' && echo "smoke: OK" || (echo "smoke: FAIL" && exit 1)

docker: ## Build the Docker image
	docker build -t cartographer .

clean: ## Remove bin/ and demo-kb/
	rm -rf bin/ demo-kb/

e2e: ## Run deterministic HTTP/CLI end-to-end scenarios
	@./test/e2e/run.sh

test-install: ## Run the network-free install.sh/Cask upgrade-repair suite and the .goreleaser.yaml guard
	@./test/install/run.sh
	@./test/install/goreleaser_guard.sh


# Isolated worktree for one plan branch. Every agent client shares the
# filesystem with its own subagents, so isolation is not free anywhere: these
# two targets are the single client-neutral way to get it, and they are why the
# implement-issue skill no longer carries four copies of the same git commands.
worktree-add: ## Isolated worktree for a plan branch: make worktree-add SLUG=my-change
	@test -n "$(SLUG)" || (echo "worktree-add: SLUG is required" && exit 1)
	@git fetch origin
	@git worktree add -b "feat/$(SLUG)" ".worktrees/$(SLUG)" origin/main
	@echo "worktree: $(CURDIR)/.worktrees/$(SLUG) on feat/$(SLUG)"

worktree-rm: ## Remove a plan worktree and its stale local branch: make worktree-rm SLUG=my-change
	@test -n "$(SLUG)" || (echo "worktree-rm: SLUG is required" && exit 1)
	@test -d ".worktrees/$(SLUG)" || (echo "worktree-rm: no worktree at .worktrees/$(SLUG)" && exit 1)
	@git worktree remove --force ".worktrees/$(SLUG)"
	@git branch -D "feat/$(SLUG)" 2>/dev/null || true
	@echo "worktree removed: .worktrees/$(SLUG)"

gate: fmt-check vet test ## Everything that must be green before a PR is opened
	@echo "gate: OK"

# One decision is one file under docs/decisions/, and the index in
# docs/decisions.md is generated from them. The generator IS the test that
# verifies it (internal/repodocs, run with -update), so the writer and the
# checker cannot drift apart.
decisions-index: ## Regenerate the decision index in docs/decisions.md
	@go test ./internal/repodocs -run TestDecisionIndexIsUpToDate -count=1 -args -update

codemap: ## Regenerate the code map in AGENTS.md from the package doc comments
	@go test ./internal/repodocs -run TestCodeMapIsUpToDate -count=1 -args -update

decisions-next: ## Print the next free decision number on disk
	@ls docs/decisions/D*.md 2>/dev/null | sed -E 's#.*/D([0-9]+)-.*#\1#' \
		| sort -n | tail -1 | awk '{print $$1 + 1}'

decisions-new: ## New decision from the template: make decisions-new N=202 SLUG=my-choice TOPIC=control-plane
	@test -n "$(N)" -a -n "$(SLUG)" -a -n "$(TOPIC)" || \
		(echo "decisions-new: N, SLUG and TOPIC are all required" && exit 1)
	@test ! -e "docs/decisions/D$(N)-$(SLUG).md" || \
		(echo "decisions-new: docs/decisions/D$(N)-$(SLUG).md already exists" && exit 1)
	@sed -e 's/^topic: .*/topic: $(TOPIC)/' -e 's/^# D<n> — .*/# D$(N) — <title>/' \
		docs/decisions/TEMPLATE.md > "docs/decisions/D$(N)-$(SLUG).md"
	@echo "docs/decisions/D$(N)-$(SLUG).md — write it, then: make decisions-index"
