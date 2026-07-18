# Contributing to Cartographer

Thanks for your interest! Cartographer is a personal project maintained on a
best-effort basis: issues and pull requests are welcome, but there is no
response-time SLA.

## Prerequisites

- Go 1.26+ (see `go.mod`)
- git (the test suite exercises real git repositories)

## Building and testing

```bash
make build    # → bin/cartographer
make test     # go test ./...
make vet      # go vet ./...
make fmt      # gofmt -w .
make smoke    # quick stdio smoke test
```

The agent-level E2E suite (`make e2e`) additionally needs OpenCode and an
OpenAI-compatible LLM endpoint (`E2E_LLM_BASE_URL`); see
[`test/e2e/README.md`](test/e2e/README.md). It is not required for regular
contributions — CI runs `make vet && make test`.

## Finding your way around

- [`CLAUDE.md`](CLAUDE.md) has a compact code map (what lives where).
- [`docs/index.md`](docs/index.md) is the documentation index, with reading
  paths and maintenance rules.
- The *why* behind non-obvious choices lives in
  [`docs/decisions.md`](docs/decisions.md) — grep the `## D<n>` entry.

## Pull requests

- Fork and open a PR against `main`. Direct pushes are disabled; every change
  goes through a PR with green CI (`make vet && make test`).
- PRs are **squash-merged** and the PR title becomes the commit message on
  `main`: it must be a valid [conventional commit](https://www.conventionalcommits.org/)
  (`feat: ...`, `fix: ...`, `docs: ...`, ...). CI enforces this. Releases are
  cut automatically from these commits by release-please, so the title you
  write is the changelog line users will read.
- **Documentation moves with the code in the same PR** — it's a project rule:
  any change touching interfaces, behavior, configuration, or architecture
  updates the corresponding `docs/` pages (see `docs/index.md` §Maintenance
  rules). Non-obvious choices get an entry in `docs/decisions.md`.
- New MCP tools follow the checklist in `CLAUDE.md` §Adding an MCP tool and
  come with tests in `internal/mcpserver/server_test.go`.
- Coding conventions: [`docs/conventions.md`](docs/conventions.md).

## Reporting bugs

Open a GitHub issue with the version (`cartographer version`), the transport
in use (stdio or HTTP), and a minimal reproduction. For suspected security
issues, see [`SECURITY.md`](SECURITY.md) — please do **not** open a public
issue.
