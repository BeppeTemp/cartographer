# Contributing to Cartographer

Thanks for your interest! Cartographer is a personal project maintained on a
best-effort basis: issues and pull requests are welcome, but there is no
response-time SLA.

## Prerequisites

- Go 1.26+ (see `go.mod`)
- git (the test suite exercises real git repositories)

## Building and testing

```bash
make gate     # gofmt + vet + test — everything that must be green before a PR
make build    # → bin/cartographer
make test     # go test ./...
make vet      # go vet ./...
make fmt      # gofmt -w .
make smoke    # quick stdio smoke test
```

The deterministic E2E suite (`make e2e`) exercises the built HTTP server, CLI
client and temporary git remotes without model credentials; see
[`test/e2e/README.md`](test/e2e/README.md). CI runs `make gate` — the same
command, so there is one answer to "what must be green" — followed by
`make smoke-http`, `make e2e` and `make test-install`.

`make test` also runs the repository's own documentation gates
(`internal/repodocs`): the size of `AGENTS.md`, the freshness of the generated
decision index, the fact that every relative documentation link resolves, and
that the skill directories have not drifted apart. They live in `make test`
rather than in a separate command on purpose — a gate you have to remember to
run is not a gate.

## Finding your way around

- [`AGENTS.md`](AGENTS.md) is the canonical entry point for both people and
  agents, and it has a compact code map (what lives where). `CLAUDE.md` is one
  line importing it.
- [`docs/index.md`](docs/index.md) is the documentation index, with reading
  paths and the maintenance rules.
- The *why* behind non-obvious choices is one file per decision under
  [`docs/decisions/`](docs/decisions.md). In code and prose the reference is the
  bare `D<n>`: resolve it with `ls docs/decisions/D47-*`.
- Project state lives in GitHub: issues for backlog and bugs, pull requests
  for delivery, releases and `CHANGELOG.md` for completed user-visible work.

## Working with an agent client

This repository is developed with coding agents and is set up so that **the
instructions exist once**. `AGENTS.md` is the only real instruction file; Codex
and Kiro read it natively, and Antigravity is documented to (see the table, and
read the caveat under it). Claude Code is the only client that will not read that
filename, so `CLAUDE.md` is a one-line `@AGENTS.md` import — an import rather than
a symlink because git does not materialise symlinks on a Windows checkout without
Developer Mode, and a public repository does not get to choose the operating
system of the people who clone it.

Nothing else is needed to start. Concretely:

| Client | Instructions | Skills | Notes |
|---|---|---|---|
| **Codex** | `AGENTS.md`, natively | `.agents/skills/`, natively | Do not add an `AGENTS.override.md`: it *replaces* `AGENTS.md` in the same directory rather than adding to it |
| **Kiro** | `AGENTS.md`, natively | `.kiro/skills/` → symlinks | Do not put a copy of `AGENTS.md` under `.kiro/steering/`: it is already always included, and a steering file that re-includes it would load it twice. A steering file with *other* content is fine — `cartographer sync` legitimately owns `.kiro/steering/cartographer.md` when this workspace is bound to a KB |
| **Claude Code** | `CLAUDE.md` → `@AGENTS.md` | `.claude/skills/` → symlinks | — |
| **Antigravity** | `AGENTS.md`, per its own documentation — not audited here | **global only**: `~/.gemini/config/skills/`; no project-local directory | Its whole configuration root is global (`~/.gemini/GEMINI.md`, `~/.gemini/config/{skills,agents,hooks,mcp_config.json}`), so the two skills below are *not* reachable from a clone and no repo-local path would make them so |

The Antigravity row is the one to be careful with. Where a client reads its
instructions and its skills is a fact this repository already owns, audited, in
`internal/provisioning/workspacescope.go` and `internal/configurator/registry.go`
(D193) — and that matrix records **no project-local cell of any kind** for
Antigravity. `TestClientSkillSurfacesMatchTheProviderRegistry` checks this table
against the matrix, so if you re-audit a client, change both together (D207).

The two skills, `plan-issue` and `implement-issue`, exist **once** in
`.agents/skills/`; `.claude/skills/<name>` and `.kiro/skills/<name>` are
symlinks into it. Symlinks do work — verified empirically on Kiro CLI 2.21.4
across 45 runs with negative controls — but **git on Windows is the catch**: with
`core.symlinks=false`, which is what a checkout without Developer Mode gets, git
materialises each link as a text file containing its target and the client then
loads **nothing at all, silently**. So:

- if you are on Windows, enable Developer Mode or use an elevated git so that
  `core.symlinks=true`, otherwise the two skills will be missing from your
  session with no error to tell you;
- `make test` asserts the **git index** mode (`120000`), not the working tree, so
  the check gives the same answer on every platform and a copy committed by
  mistake fails CI.

If you add a skill, write its frontmatter to Kiro's rules — `name` present, equal
to the directory name, lowercase kebab-case, ≤64 characters; `description` ≤1024
characters with the use case and the trigger words **first** — and it will be
valid on all four. Every client shortens or drops the descriptions that do not
fit its listing budget, starting from the end.

**Keep the frontmatter valid YAML, and mind `": "` in particular.** An unquoted
value containing a colon followed by a space is a YAML syntax error, and what a
client does with it is drop the skill and list the others — no message, nothing
in a log. `implement-issue` shipped that way and was invisible to Kiro until a
gate caught it; `make test` now parses every `SKILL.md` with the same
spec-compliant parser a client uses (D207).

MCP configuration is **not** in the repository: each client keeps it in its own
format and location — project-local for `.mcp.json`, `.codex/config.toml` and
`.kiro/settings/mcp.json`, global-only for `~/.gemini/config/mcp_config.json` —
all of them machine-local and git-ignored. If you are pointing a client at a
Cartographer server, use `cartographer connect`, not a hand-written file.

Working on more than one plan at a time? No client isolates its own subagents
from your working copy, so use a worktree per plan:

```bash
make worktree-add SLUG=my-change    # branches feat/my-change from origin/main
make worktree-rm  SLUG=my-change    # after the PR is merged; --force, so check first
```

## Pull requests

- Fork and open a PR against `main`. Direct pushes are disabled; every change
  goes through a PR with green CI (`make gate` locally first).
- PRs are **squash-merged** and the PR title becomes the commit message on
  `main`: it must be a valid [conventional commit](https://www.conventionalcommits.org/)
  (`feat: ...`, `fix: ...`, `docs: ...`, ...). CI enforces this. Releases are
  cut automatically from these commits by release-please, so the title you
  write is the changelog line users will read.
- The project is in **beta** (pre-1.0): breaking changes bump the **minor**
  version (release-please `bump-minor-pre-major`), so any 0.x minor release
  may break compatibility. GitHub releases are marked as **pre-releases**
  until 1.0.0, which will be tagged once the MCP tool surface and the CLI
  stabilize.
- **Documentation moves with the code in the same PR** — it's a project rule:
  any change touching interfaces, behavior, configuration, or architecture
  updates the corresponding `docs/` pages (see `docs/index.md`
  §Documentation maintenance rules). Non-obvious choices get **one** decision
  file: `make decisions-new N=<n> SLUG=<slug> TOPIC=<topic>`, write it, then
  `make decisions-index`. CI fails if the index is stale.
- New MCP tools follow the checklist in
  [`internal/mcpserver/AGENTS.md`](internal/mcpserver/AGENTS.md) §Adding a tool
  and come with tests in `internal/mcpserver/server_test.go`.
- Coding conventions: [`docs/conventions.md`](docs/conventions.md).

## Plan issues (design → implementation handoff)

Non-trivial changes start as a **plan issue**: a GitHub issue created from the
`Plan` template (label `plan`) that packages the outcome of an analysis/design
discussion into a self-contained implementation plan. The pattern: one session
(or person) analyzes and decides; a separate one implements. The issue is the
**only bridge** between the two — the implementer does not see the analysis
discussion.

Before designing, survey the **open plan issues** (label `plan`) as well as the
code: a request already covered by an open plan or already implemented on
`main` is reported, not re-planned; a partial overlap is reconciled by amending
the existing issue or by stating the relationship (execution order, shared
files) in the new one.

Self-sufficiency test (determines the level of detail): a fresh session with
only the issue and the repo must be able to implement without asking questions.

- Every non-obvious decision is **already made and justified in one line** —
  the implementer does not relitigate it.
- No open questions; anything delegated to the implementer is explicitly
  marked and is only a detail (local naming, test order).
- Expected errors and edge cases are listed with the desired behavior.
- **No code in the plan**: exact semantics plus real `file:line` pointers,
  derived from the code before writing — not paraphrases of it.
- A single analysis may yield **several plan issues**: each one states the
  cross-plan execution order and which sibling plans touch the same files
  (those land sequentially, never in parallel). Amendments made before
implementation starts go in the issue body; later ones in comments.

A plan title reserves its `D<n>` number. `make decisions-next` gives the highest
number on disk plus one, but that is only half the answer: an open plan issue
reserves its number before any file exists, so also check
`gh issue list --label plan --state all --limit 1000` and take the number above
both maxima. The issue's Closing section names the topic the decision file will
declare.

The implementation PR references the issue (`Closes #<n>`), executes the work
packages in order (`make gate` green after each), updates the docs per the
issue's closing checklist, and adds the decision file
`docs/decisions/D<n>-<slug>.md` followed by `make decisions-index`. The file
records the implemented choice and any deviation from the plan; it does not
duplicate current-state documentation. If the plan contradicts the actual code,
stop and flag it in an issue comment: the plan may be stale relative to `main`.

## Reporting bugs

Open a GitHub issue with the version (`cartographer version`), the transport
in use (stdio or HTTP), and a minimal reproduction. For suspected security
issues, see [`SECURITY.md`](SECURITY.md) — please do **not** open a public
issue.
