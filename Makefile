.PHONY: help build test race vet fmt fmt-check run run-http smoke smoke-http docker docker-smoke clean e2e demo-image demo-smoke demo-worker-test demo-tf-check test-install worktree-add worktree-rm dev gate decisions-index changelog-areas decisions-new codemap settings-example web web-test web-check e2e-web hero

VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)

# gofmt takes paths, and `gofmt -l .` walks every hidden directory — including
# .worktrees/, where a sibling plan's subagent is working. That made `make gate`
# fail on someone else's unfinished code and `make fmt` rewrite it, which is
# exactly what the cartographer-implement-plan mandate forbids. Naming the two directories
# that hold this module's Go code scopes both targets correctly, and unlike
# `git ls-files` it still sees a file that has been written but not yet staged —
# a gate that passes locally and fails in CI is worse than no gate (D209).
GO_DIRS = cmd internal

help: ## Show this message
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*##"}; {printf "  %-12s %s\n", $$1, $$2}'

build: ## Build the binary into bin/cartographer
	go build -ldflags "-X main.version=$(VERSION)" -o bin/cartographer ./cmd/cartographer

test: ## Run all tests
	# 20m, not go test's default 10m: internal/mcpserver alone takes ~10m on the
	# Windows runner (git is slow there), and the timeout then kills the package.
	go test -timeout 20m ./...

race: ## Run the concurrency tests (name contains Race or Concurrent) under the race detector
	# A data race seldom fails an assertion: only -race reports it. The whole
	# suite under -race is several times slower, so only the tests written to
	# exercise concurrency run here; name a new one accordingly.
	go test -race -count=1 -run 'Race|Concurren' ./...

vet: ## Run go vet (also for Windows: test-windows skips PRs outside platform-specific code, so a PR catches its compile errors here)
	go vet ./...
	GOOS=windows go vet ./...

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

run: build demo-kb ## Start the stdio server with a demo KB
	./bin/cartographer serve --kb ./demo-kb

run-http: build demo-kb ## Start the HTTP server on 127.0.0.1:39273 with a demo KB
	./bin/cartographer serve --kb ./demo-kb --http 127.0.0.1:39273

demo-kb: ## Create the demo KB `run`, `run-http` and `smoke` serve (serve never creates one, D906)
	@test -d demo-kb || ./bin/cartographer kb create demo-kb --no-remote --local --data . >/dev/null

smoke-http: build ## HTTP flow smoke test: creates KB, archives, dossiers via MCP
	@./test/smoke/http.sh

smoke: build demo-kb ## Build + quick stdio test
	@# stdin is held open past the request: in MCP stdio the client owns the
	@# pipe's lifetime, and the server tears the session down on EOF. Closing
	@# it immediately (a bare `echo |`) races the response out of existence.
	@{ echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'; sleep 1; } | \
		./bin/cartographer serve --kb ./demo-kb 2>/dev/null | \
		grep -q '"protocolVersion"' && echo "smoke: OK" || (echo "smoke: FAIL" && exit 1)

web: ## Build the embedded Atlas UI bundle into internal/webui/dist (needs Node, see web/.nvmrc)
	@cd web && npm ci --silent && npm run build
	@go run ./internal/webui/gen ./web ./internal/webui/dist

web-test: ## Run the frontend unit/component tests (needs Node)
	@cd web && npm ci --silent && npm test

# A clean locked build from the committed lockfile, plus one check on the
# provenance manifest. It deliberately does NOT diff the rest of the bundle:
# Vite output is not byte-stable across Node patch releases, so comparing it
# would fail for reasons unrelated to the change. The manifest is a hash of the
# sources and is byte-stable, so this catches a manifest edited by hand, while
# internal/webui's Go test catches sources changed without a rebuild. The
# budget script fails the job when the compressed bundle outgrows 700 KiB.
web-check: web ## Clean locked frontend build + provenance and size-budget checks (CI only; needs Node)
	@git diff --quiet -- internal/webui/dist/provenance.json || { \
		echo "web-check: provenance.json does not match the sources it claims."; \
		git --no-pager diff -- internal/webui/dist/provenance.json; \
		exit 1; \
	}
	@cd web && npm run --silent budget
	@echo "web-check: OK"

# Not part of gate: it needs Node and Playwright's Chromium, and gate stays
# Go-only (D227). The harness builds the binary itself and tests it serving
# its embedded bundle, never Vite's dev server (D228).
e2e-web: ## Browser-level Atlas UI suite against the built binary (needs Node + `npx playwright install chromium`; E2E_WEB_ARGS passes Playwright flags)
	@cd web && npm ci --silent
	@web/e2e/run.sh $(E2E_WEB_ARGS)

# Records the README animation (D330) into ./hero.webp, a gitignored preview:
# headless and without a GPU, on a virtual clock (D790). The README's copy is
# recorded by the `hero` workflow on each release that changes web/ and lives on
# the readme-assets branch; hero.spec.ts itself runs in e2e-web.
hero: ## Record the README's Atlas animation to ./hero.webp (needs Node, Chromium, ffmpeg, img2webp; no GPU)
	@cd web && npm ci --silent
	@web/scripts/record-hero.sh

docker: ## Build the Docker image
	docker build -t cartographer .

docker-smoke: docker ## Build the image, then check its tools, user and a served write (test/smoke/image.sh, D995)
	test/smoke/image.sh cartographer

DEMO_IMAGE ?= cartographer-demo
# Override with BASE=cartographer (after `make docker`) to test the working tree.
DEMO_BASE ?= ghcr.io/beppetemp/cartographer:latest

demo-image: ## Build the live-demo image (D751) on the released server image
	docker build -f deploy/demo/Dockerfile --build-arg BASE=$(DEMO_BASE) -t $(DEMO_IMAGE) .

demo-smoke: demo-image ## Run the demo image: read ok, write refused, guestbook write lands in its remote, no token 401
	deploy/demo/smoke.sh $(DEMO_IMAGE)

demo-worker-test: ## Test the demo Worker's moderation logic (D800), Node only
	node --test deploy/demo/worker.test.mjs

demo-tf-check: ## terraform fmt + validate of deploy/demo (no credentials)
	cd deploy/demo && terraform fmt -check && terraform init -backend=false -input=false >/dev/null && terraform validate

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
# cartographer-implement-plan skill no longer carries four copies of the same git commands.
worktree-add: ## Isolated worktree for a plan branch (from upstream/main when that remote exists, else origin/main): make worktree-add SLUG=my-change
	@test -n "$(SLUG)" || (echo "worktree-add: SLUG is required" && exit 1)
	@./scripts/upstream-repo.sh --require-remote >/dev/null
	@base=origin; git remote get-url upstream >/dev/null 2>&1 && base=upstream; \
	git fetch $$base && git worktree add -b "feat/$(SLUG)" ".worktrees/$(SLUG)" $$base/main
	@echo "worktree: $(CURDIR)/.worktrees/$(SLUG) on feat/$(SLUG)"

worktree-rm: ## Remove a plan worktree and its stale local branch: make worktree-rm SLUG=my-change
	@test -n "$(SLUG)" || (echo "worktree-rm: SLUG is required" && exit 1)
	@test -d ".worktrees/$(SLUG)" || (echo "worktree-rm: no worktree at .worktrees/$(SLUG)" && exit 1)
	@git worktree remove --force ".worktrees/$(SLUG)"
	@git branch -D "feat/$(SLUG)" 2>/dev/null || true
	@echo "worktree removed: .worktrees/$(SLUG)"

# The live session's instance (the cartographer-develop-live skill): Go rebuilt and restarted on
# change, the UI hot-reloaded by Vite. DATA is copied, never served in place.
dev: ## Live instance from this worktree: make dev [DATA=<data dir>] [API_PORT=39280] [UI_PORT=5173]
	@DATA="$(DATA)" API_PORT="$(API_PORT)" UI_PORT="$(UI_PORT)" ./scripts/dev.sh

# Deliberately Go-only: a contributor changing Go code must not need a Node
# toolchain. The committed UI bundle is kept honest by a pure-Go staleness test
# in internal/webui, which runs here; building and testing the frontend is the
# `web` CI job's business (D227).
gate: fmt-check vet test race ## Everything that must be green before a PR is opened
	@echo "gate: OK"

# One decision is one file under docs/decisions/, and the index in
# docs/decisions.md is generated from them. The generator IS the test that
# verifies it (internal/repodocs, run with -update), so the writer and the
# checker cannot drift apart.
decisions-index: ## Regenerate the decision index in docs/decisions.md
	@go test ./internal/repodocs -run TestDecisionIndexIsUpToDate -count=1 -args -update

# The release PR job runs this after release-please has rewritten CHANGELOG.md
# (D865); it is idempotent, so running it by hand on a grouped file changes nothing.
changelog-areas: ## Group the newest CHANGELOG.md section by area (the release PR job runs it, D865)
	@go run ./internal/changelog/gen -file CHANGELOG.md

codemap: ## Regenerate the code map in AGENTS.md from the package doc comments
	@go test ./internal/repodocs -run TestCodeMapIsUpToDate -count=1 -args -update

settings-example: ## Regenerate config.example.yaml from the settings schema (internal/config/schema.go)
	@SETTINGS_EXAMPLE_UPDATE=1 go test ./internal/config -run TestExampleIsUpToDate -count=1

decisions-new: ## New decision from the template: make decisions-new N=<plan issue or PR number> SLUG=my-choice TOPIC=control-plane (D672)
	@test -n "$(N)" -a -n "$(SLUG)" -a -n "$(TOPIC)" || \
		(echo "decisions-new: N, SLUG and TOPIC are all required" && exit 1)
	@grep -q '{"$(TOPIC)",' internal/repodocs/repodocs.go || \
		(echo "decisions-new: unknown TOPIC $(TOPIC); valid: $$(grep -oE '^	\{"[a-z-]+"' internal/repodocs/repodocs.go | tr -d '{"	' | tr '\n' ' ')" && exit 1)
	@test ! -e "docs/decisions/D$(N)-$(SLUG).md" || \
		(echo "decisions-new: docs/decisions/D$(N)-$(SLUG).md already exists" && exit 1)
	@sed -e 's/^topic: .*/topic: $(TOPIC)/' -e 's/^# D<n> — .*/# D$(N) — <title>/' \
		docs/decisions/TEMPLATE.md > "docs/decisions/D$(N)-$(SLUG).md"
	@echo "docs/decisions/D$(N)-$(SLUG).md — write it, then: make decisions-index"
