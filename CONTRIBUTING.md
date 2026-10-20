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
make vet      # go vet ./..., also GOOS=windows
make fmt      # gofmt -w .
make smoke    # quick stdio smoke test
```

The deterministic E2E suite (`make e2e`) exercises the built HTTP server, CLI
client and temporary git remotes without model credentials; see
[`test/e2e/README.md`](test/e2e/README.md). CI runs `make gate` — the same
command, so there is one answer to "what must be green" — followed by
`make smoke-http`, `make e2e` and `make test-install`.

The Atlas UI has its own loop (Node, outside `make gate`): `make web-test` for
the frontend tests, `make web` to rebuild the committed bundle, `make e2e-web`
for the browser suite — see [`docs/testing.md`](docs/testing.md) §Frontend. The
README animation needs nothing by hand: the release that changes `web/`
re-records it (`make hero` previews it locally).

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
- The top level: `cmd/cartographer/` is the single binary (server, client, TUI),
  `internal/` has one package per concern, `web/` is the Atlas UI source,
  `docs/` the documentation, `test/` the shell-driven smoke and E2E suites.
- [`docs/index.md`](docs/index.md) is the documentation index, with reading
  paths and the maintenance rules.
- The *why* behind non-obvious choices is one file per decision under
  [`docs/decisions/`](docs/decisions.md). In code and prose the reference is the
  bare `D<n>`: resolve it with `ls docs/decisions/D47-*`.
- Project state lives in GitHub: issues for backlog and bugs, pull requests
  for delivery, releases and `CHANGELOG.md` for completed user-visible work.

## Working with an agent client

This repository is developed with coding agents and is set up so that **the
instructions exist once**. `AGENTS.md` is the only real instruction file; Kiro,
OpenCode and Copilot CLI read it natively (see the table). Claude Code is
the only client that will not read that filename, so `CLAUDE.md` is a one-line `@AGENTS.md` import — an import rather than
a symlink because git does not materialise symlinks on a Windows checkout without
Developer Mode, and a public repository does not get to choose the operating
system of the people who clone it.

Nothing else is needed to start. Concretely:

| Client | Instructions | Skills | Notes |
|---|---|---|---|
| **Kiro** | `AGENTS.md`, natively | `.kiro/skills/` → symlinks | Do not put a copy of `AGENTS.md` under `.kiro/steering/`: it is already always included, and a steering file that re-includes it would load it twice. A steering file with *other* content is fine — `cartographer sync` legitimately owns `.kiro/steering/cartographer.md` when this workspace is bound to a KB |
| **Claude Code** | `CLAUDE.md` → `@AGENTS.md` | `.claude/skills/` → symlinks | Every directory with an `AGENTS.md` has its own one-line `CLAUDE.md` import, which Claude loads on its first read of a file there (D213) |
| **OpenCode** | `AGENTS.md`, natively | `.opencode/skills/` → symlinks | It does **not** load `.claude/agents/`: its project agents are `.opencode/agents/`, and `opencode debug agents` from the repository root lists them (run `opencode reload` after editing one: a background service caches the configuration). Probed on 2.0.30: it also finds `.agents/skills/` and lists each skill once, `.opencode/skills/` winning; the bridge stays because it is the documented project cell |
| **Copilot CLI** (`copilot`) | `AGENTS.md`, natively ("Repository instructions", probed on 1.0.94) | `.agents/skills/`, natively ("Project skills") | Its project agents, `.github/agents/<name>.agent.md`, load only once the folder is trusted (`copilot --agent <name>` selects one); the `model` key is honoured, and a model the subscription does not offer falls back to `auto` with a warning (probed on 1.0.98). Its project `mcp` and hook locations (`.mcp.json`, `.github/hooks`) are gated on folder trust and are not used (D676) |

Where a client reads its instructions and its skills is a fact this repository
already owns, audited, in `internal/provisioning/workspacescope.go` and
`internal/configurator/registry.go` (D193, D364).
`TestClientSkillSurfacesMatchTheProviderRegistry` checks this table against the
matrix, so if you re-audit a client, change both together (D207).

The repo-local skills (`plan-issue`, `implement-issue`, `live-dev`, `review-pr`, `harness-watch`: the directories of `.agents/skills/`) exist **once** in
`.agents/skills/`; `.claude/skills/<name>`, `.kiro/skills/<name>` and
`.opencode/skills/<name>` are symlinks into it. Symlinks do work — verified empirically on Kiro CLI 2.21.4
across 45 runs with negative controls — but **git on Windows is the catch**: with
`core.symlinks=false`, which is what a checkout without Developer Mode gets, git
materialises each link as a text file containing its target and the client then
loads **nothing at all, silently**. So:

- if you are on Windows, enable Developer Mode or use an elevated git so that
  `core.symlinks=true`, otherwise the skills will be missing from your
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
format and location — project-local for `.mcp.json`, `opencode.json` and
`.kiro/settings/mcp.json` — all of them machine-local and git-ignored. If you are pointing a client at a
Cartographer server, use `cartographer connect`, not a hand-written file.

The repository also ships five subagents: `plan-implementer`, the worker the
`implement-issue` skill spawns per plan, `live-implementer`, the one the
`live-dev` skill spawns per change, `pr-reviewer`, the read-only reviewer
the `review-pr`, `implement-issue` and `live-dev` skills spawn per pull request,
`security-reviewer`, the read-only attacker's-view pass they add on full-tier PRs,
and `reader`, the read-only reader of one voluminous source (the failed log of a
red CI run, a client's release notes) that returns a digest instead of the text.
Each exists in each client's own format — `.claude/agents/<name>.md`,
`.kiro/agents/<name>.json`, `.opencode/agents/<name>.md` and
`.github/agents/<name>.agent.md` — and the four formats must say the same thing:
the same prompt body and description, on a Claude model in the client's own
spelling (Claude Code `claude-sonnet-5-5`, Kiro and Copilot CLI
`claude-sonnet-5.5`, OpenCode `anthropic/claude-sonnet-5-5`; `reader` runs on
Haiku 5.5, the other four on Sonnet 5.5). `TestRepoSubagentsSayTheSameThingOnEveryClient`
checks all of it, and refuses an agent file in a client directory that has no
`.claude/agents` counterpart. The procedure itself stays in the skill (D327, D884).

Working on more than one plan at a time? No client isolates its own subagents
from your working copy, so use a worktree per plan:

```bash
make worktree-add ISSUE=123 SLUG=my-change   # branches feat/123-my-change from origin/main
make worktree-rm  ISSUE=123 SLUG=my-change   # after the PR is merged; --force, so check first
```

A branch is `<type>/<issue>-<slug>` (D926): `TYPE=fix` for a bug, `TYPE=hot-fix`
for an urgent fix of a released version, `feat` (the default) for anything else.
The PR title still carries the exact conventional-commit type. Only a live
session, which has no issue until its draft PR, leaves `ISSUE` out.

## Adding a lint check

A check is declared once, in `internal/lint/registry.go` (D354); every table that used to list
checks by hand (who may accept it, whether `kb_repair` can fix it, whether `auto_repair` may run it,
the Artifacts panel, what a write response evaluates) is derived from that entry. Three edits, nothing else:

1. **Register the spec**: a `CheckSpec` with its `Name`, default `Severity`, `Level`, `Accept`, its
   reader's `Category` (D365: the Atlas Health panel lists every check by it; give the check a
   plain-words label in `web/src/lib/health.ts` too), and the flags that apply (`FixKinds`, `AutoRepairSafe`, `OnWrite`, `Judgement`…). A `LevelConcept` check
   is computed in `conceptFindings` (`internal/lint/conceptcheck.go`), which `lint` and the write path
   share; graph, map and artifact checks keep their dedicated pass and only register here.
2. **Emit through the constructor**: `newFinding("<name>", Finding{Path: …, Message: …})`. It fills the
   severity from the spec (an unregistered name panics under test); a `Finding{…}` literal that sets
   `Check` fails `TestNoFindingLiteralOutsideRegistry`.
3. **Regenerate the catalogue** in `docs/data-plane.md`:
   `go test ./internal/repodocs -run TestLintCatalogueIsUpToDate -args -update`, then add the prose
   that explains the check where its family is described.

A check that only runs when the KB gives it something to compare against (a glossary, a contract key…)
also gets its precondition in `lint.Inactive` (`internal/lint/activity.go`, D371), so `/checks` and the
Health coverage say "not checked here" instead of a zero; never make a check silent by an opt-in.

`make gate` fails until all three are done. The check's decision (`make decisions-new`) is the usual one.

## Pull requests

- Fork and open a PR against `main`. Direct pushes are disabled; every change
  goes through a PR with green CI (`make gate` locally first).
- **Review costs more than writing**, so a PR is shaped to be cheap to review
  (D856). Three rules, checked by `.agents/skills/review-pr/scripts/triage.sh`
  — run it on your branch before opening:
  - a contributor's change over **150 production lines** (tests, docs and
    generated files excluded), or one that changes a public contract (MCP tool
    schema, CLI flag, config key, on-disk format), closes a **plan issue the
    maintainer approved** — labelled `plan` and `approved`. The design is
    reviewed there, before any code exists; a trivial fix needs only `Closes #<n>`;
  - at most **500 production lines added** per PR: a larger change is split by
    work package into PRs that land in order, or the PR body has a line starting
    `Size exception:` that says why it cannot be;
  - the body follows `.github/pull_request_template.md`: its `Self-review`
    section reports an independent review run before opening — the
    `pr-reviewer` subagent of the `review-pr` skill, or your client's equivalent,
    at most three rounds — and its `Revert` section says what a revert of the
    released change would not undo. A `fix:` PR that repairs a regression also
    names the PR that introduced it, on a line `Regression-of: #<n>`: the
    reviewers are scored against such PRs (`review-pr` §When a defect escapes).
- PRs are **squash-merged** and the PR title becomes the commit message on
  `main`: it must be a valid [conventional commit](https://www.conventionalcommits.org/)
  (`feat: ...`, `fix: ...`, `docs: ...`, ...). CI enforces this. Releases are
  cut automatically from these commits by release-please, so the title you
  write is the changelog line users will read.
  - **Scope** (optional, enforced): one of `atlas`, `mcp`, `lint`, `client`, `kb`,
    `dev`, `demo`, `deploy`, `ci`, `docs`, `deps` or `main`. The release changelog
    is grouped by area from it (D865): `atlas` is **Atlas**, `mcp` **MCP tools**,
    `lint` **Lint & repair**, `client` **Client & CLI**, `kb` **Server & KB**;
    an unscoped entry is **General**. `dev`, `demo`, `deploy` and `ci` entries
    do not appear in the changelog (they are not changes a user installs). The
    older spellings (`webui`, `mcpserver`, `skills`, `gitx`, …) still group
    correctly on `main`, but a title can no longer use them.
  - **Body lines**: a line of the PR body that starts like a conventional commit
    (`feat(mcp): ...`) becomes a changelog entry of its own, and release-please
    keeps only its **first physical line**: write it as one unwrapped line, never
    hard-wrapped at 72 columns. CI fails the PR otherwise (`pr-title`).
- The project is in **beta** (pre-1.0): breaking changes bump the **minor**
  version (release-please `bump-minor-pre-major`), so any 0.x minor release
  may break compatibility. GitHub releases are marked as **pre-releases**
  until 1.0.0, which will be tagged once the MCP tool surface and the CLI
  stabilize.
- **Documentation moves with the code in the same PR** — it's a project rule:
  any change touching interfaces, behavior, configuration, or architecture
  updates the corresponding `docs/` pages (see `docs/index.md`
  §Documentation maintenance rules). An **architectural or contract** choice
  (an invariant, an interface, a rejected alternative a reader would otherwise
  re-propose) gets **one** decision file, numbered after its plan issue or,
  without one, after the PR (D672): `make decisions-new N=<n> SLUG=<slug> TOPIC=<topic>`.
  Smaller choices need no file — the plan issue and the PR that closes it are
  their record. Do not regenerate `docs/decisions.md` in a feature PR: the
  release PR regenerates the index (D328).
- New MCP tools follow the checklist in
  [`internal/mcpserver/AGENTS.md`](internal/mcpserver/AGENTS.md) §Adding a tool
  and come with tests in `internal/mcpserver/server_test.go`.
- Coding conventions: [`docs/conventions.md`](docs/conventions.md).

## Issues: idea, bug, plan

An issue is the backlog. Open one as soon as something is worth coming back to,
from the template that fits:

- **Idea** (label `idea`): a few lines on what should be possible and why. No
  design needed.
- **Bug** (label `bug`): version, transport, what happened, a reproduction.
- **Plan** (label `plan`): a self-contained implementation plan, below.

When an idea or a bug is picked up for implementation, it is **promoted in
place**: its body is rewritten from the Plan template and it gets the `plan`
label, so the discussion and the plan stay in one issue. A trivial fix needs no
plan: a PR that says `Closes #<n>` is enough. A plan written by a contributor
is implemented once the maintainer has added the `approved` label, which only
someone with triage rights can set — the template sets `plan` for anyone.

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
- **No private names**: measurements and reproductions from a real setup use placeholder
  KB names, hosts and users — the repository is public (D259).
- **No code in the plan**: exact semantics plus real `file:line` pointers,
  derived from the code before writing — not paraphrases of it.
- A single analysis may yield **several plan issues**: each one states the
  cross-plan execution order and which sibling plans touch the same files
  (those land sequentially, never in parallel). Amendments made before
implementation starts go in the issue body; later ones in comments.

A plan's decision number is its issue number (D672): nothing to reserve, and
two sessions planning in parallel cannot collide. The issue's Closing section
says whether the plan makes a choice worth a decision file, and its topic.

The implementation PR references the issue (`Closes #<n>`), executes the work
packages in order (`make gate` green after each), updates the docs per the
issue's closing checklist, and, when the checklist names one, adds the decision
file `docs/decisions/D<issue>-<slug>.md` (the index is regenerated by the release PR, D328). The file
records the implemented choice and any deviation from the plan; it does not
duplicate current-state documentation. If the plan contradicts the actual code,
stop and flag it in an issue comment: the plan may be stale relative to `main`.

## Live sessions (no plan issue)

When the outcome is found by trying it — the Atlas UI, a UX detail, CLI output to
tune — design and implementation stay in one session, with the user trying each
step on a running instance. The procedure, the roles and the subagent mandate are
in the `live-dev` skill (`.agents/skills/live-dev/SKILL.md`). What is specific
to it:

```bash
make worktree-add SLUG=my-change
cd .worktrees/my-change && make dev DATA=<data dir>   # or no DATA: the demo KBs in demo/
```

`make dev` rebuilds and restarts the server on every Go change and serves the UI
with hot reload on `http://localhost:5173/ui/`. `DATA` is copied with its git
remotes removed and git sync off, so nothing the session writes leaves the
machine; `API_PORT`/`UI_PORT` keep two sessions apart. The PR is opened as a
draft after the first accepted step, and its number is the decision number
(D672). With no plan issue, the PR is the record: its title (`feat:`/`fix:` for
what a user notices) and its body become the squashed commit, and so the
release changelog — the skill says what the body must hold. Everything else — `make gate`, docs and decision in the same PR, an
independent read of the diff before the merge — is the same as for a plan.

## Reporting bugs

Open a GitHub issue from the **Bug** template: the version (`cartographer version`),
the transport in use (stdio or HTTP), and a minimal reproduction. For suspected security
issues, see [`SECURITY.md`](SECURITY.md) — please do **not** open a public
issue.
