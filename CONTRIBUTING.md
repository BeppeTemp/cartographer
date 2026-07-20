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

- [`AGENTS.md`](AGENTS.md) has a compact code map (what lives where). It is
  the canonical instructions file for coding agents (`CLAUDE.md` is a symlink
  to it).
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
- The project is in **beta** (pre-1.0): breaking changes bump the **minor**
  version (release-please `bump-minor-pre-major`), so any 0.x minor release
  may break compatibility. 1.0.0 will be tagged once the MCP tool surface and
  the CLI stabilize.
- **Documentation moves with the code in the same PR** — it's a project rule:
  any change touching interfaces, behavior, configuration, or architecture
  updates the corresponding `docs/` pages (see `docs/index.md` §Maintenance
  rules). Non-obvious choices get an entry in `docs/decisions.md`.
- New MCP tools follow the checklist in `AGENTS.md` §Adding an MCP tool and
  come with tests in `internal/mcpserver/server_test.go`.
- Coding conventions: [`docs/conventions.md`](docs/conventions.md).

## Plan issues (design → implementation handoff)

Non-trivial changes start as a **plan issue**: a GitHub issue created from the
`Plan` template (label `plan`) that packages the outcome of an analysis/design
discussion into a self-contained implementation plan. The pattern: one session
(or person) analyzes and decides; a separate one implements. The issue is the
**only bridge** between the two — the implementer does not see the analysis
discussion.

Self-sufficiency test (determines the level of detail): a fresh session with
only the issue and the repo must be able to implement without asking questions.

- Every non-obvious decision is **already made and justified in one line** —
  the implementer does not relitigate it.
- No open questions; anything delegated to the implementer is explicitly
  marked and is only a detail (local naming, test order).
- Expected errors and edge cases are listed with the desired behavior.
- **No code in the plan**: exact semantics plus real `file:line` pointers,
  derived from the code before writing — not paraphrases of it.

The implementation PR references the issue (`Closes #<n>`), executes the work
packages in order (`make vet && make test` green after each), updates the docs
per the issue's closing checklist and adds the `D<n>` entry to
`docs/decisions.md`. If the plan contradicts the actual code, stop and flag it
in an issue comment: the plan may be stale relative to `main`.

## Reporting bugs

Open a GitHub issue with the version (`cartographer version`), the transport
in use (stdio or HTTP), and a minimal reproduction. For suspected security
issues, see [`SECURITY.md`](SECURITY.md) — please do **not** open a public
issue.
