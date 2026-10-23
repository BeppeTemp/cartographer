# Contributing to Cartographer

Thanks for your interest! Cartographer is a personal project maintained on a
best-effort basis: issues and pull requests are welcome, but there is no
response-time SLA.

## Prerequisites

- Go 1.26+ (see `go.mod`)
- git (the test suite exercises real git repositories)

## Building and testing

```bash
make gate     # gofmt + vet + test + race — everything that must be green before a PR
make build    # → bin/cartographer
make test     # go test ./...
make vet      # go vet ./..., also GOOS=windows
make fmt      # gofmt -w cmd internal
make smoke    # quick stdio smoke test
```

The deterministic E2E suite (`make e2e`) exercises the built HTTP server, CLI
client and temporary git remotes without model credentials; see
[`test/e2e/README.md`](test/e2e/README.md). CI runs `make gate` — the same
command, so there is one answer to "what must be green" — followed by
`make smoke-http`, `make smoke`, `make e2e` and `make test-install`.

The Atlas UI has its own loop (Node, outside `make gate`): `make web-test` for
the frontend tests, `make web` to rebuild the committed bundle, `make e2e-web`
for the browser suite — see [`docs/testing.md`](docs/testing.md) §Frontend. The
README animation needs nothing by hand: the release that changes `web/`
re-records it (`make hero` previews it locally), and the release candidate re-renders the
launch film when `film/` or what it imports changed (`film/README.md`).

`make test` also runs the repository's own documentation gates
(`internal/repodocs`): the size of `AGENTS.md`, the fact that every relative
documentation link and `#fragment` resolves, that every `<page>.md §Heading`
citation names a heading of the page, and that every skill's frontmatter is valid
for the strictest client. (The freshness of the generated decision index is checked on
the release PR only.) They live in `make test`
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

This repository is developed with **Claude Code** only (D992). `AGENTS.md` is the
canonical instruction file and `CLAUDE.md` is a one-line `@AGENTS.md` import (an
import rather than a symlink, because git does not materialise symlinks on a
Windows checkout without Developer Mode). Every directory with an `AGENTS.md` has
its own one-line `CLAUDE.md` import, which Claude loads on its first read of a
file there (D213). The skills are real directories in `.claude/skills/` and the
subagents are the files in `.claude/agents/`.

With another agent, have it read `AGENTS.md` and follow `.claude/skills/<name>/SKILL.md`
as a plain procedure, spawning a subagent from the body of `.claude/agents/<name>.md`
where it can and doing the step itself where it cannot. The repository keeps no tested
path for it. This concerns how the repository is developed; the clients the product
supports are in `docs/harnesses.md`.

### Roles: contributor and maintainer

The same skills serve two roles, and the role is your **GitHub permission on the
upstream repository** (D955): `ADMIN`, `MAINTAIN` or `WRITE` is a maintainer;
`TRIAGE`, `READ` (and anyone who works from a fork) is a contributor. The skills
check it (`gh repo view <upstream> --json viewerPermission`) and say which mode
they are in.

- A **contributor's** agent implements one *approved* plan, opens the PR from the
  fork and stops: it never merges, never arms auto-merge, never sends a review
  verdict, never edits labels and never files issues for others. `cartographer-review-pr` is
  used only before opening your own PR.
- A **maintainer's** agent runs the full flow (waves of plans, review rounds,
  ordered merge), but still implements only plans carrying `approved`, its own
  included, and adds that label only when the maintainer says so in the session.
- The reviewer agents read `AGENTS.md`, `SECURITY.md` and the skills from the
  **base branch**, never from the PR head: a PR that edits them is reviewed as a
  change, not obeyed.

The repo-local skills are `cartographer-propose-plan`, `cartographer-implement-plan`, `cartographer-develop-live`, `cartographer-review-pr` and `cartographer-audit-clients`, in `.claude/skills/`.

**Naming** (D957). Every skill and agent of this repository carries the
`cartographer-` prefix, so it does not collide with another project's in a user's
catalogue. A skill is verb-object and says what you do (`cartographer-review-pr`);
an agent is a role noun that pairs with its skill (`cartographer-contract-reviewer`).
The one word for an agent program is **client**. A description starts with
"Use when …" and the trigger words, then says what it does, in the imperative,
with " — " for asides, and lists every skill that spawns the agent.

If you add a skill, write its frontmatter to the strictest agentskills.io rules — `name` present, equal
to the directory name, lowercase kebab-case, ≤64 characters; `description` ≤1024
characters with the use case and the trigger words **first** — so the file stays
valid for any client that reads it. A client shortens or drops the descriptions that do not
fit its listing budget, starting from the end.

**Keep the frontmatter valid YAML, and mind `": "` in particular.** An unquoted
value containing a colon followed by a space is a YAML syntax error, and what a
client does with it is drop the skill and list the others — no message, nothing
in a log. `make test` parses every `SKILL.md` with the same
spec-compliant parser a client uses (D207).

MCP configuration is **not** in the repository: it is machine-local and git-ignored
(`.mcp.json`). If you are pointing a client at a Cartographer server, use
`cartographer connect`, not a hand-written file.

The repository ships its subagents in `.claude/agents/` (`ls .claude/agents`): `cartographer-plan-writer`, the read-only drafter the
`cartographer-propose-plan` skill spawns per plan once the maintainer has taken its design decisions,
`cartographer-plan-implementer`, the worker the
`cartographer-implement-plan` skill spawns per plan, `cartographer-live-implementer`, the one the
`cartographer-develop-live` skill spawns per change, `cartographer-contract-reviewer`, the read-only reviewer
the `cartographer-review-pr`, `cartographer-implement-plan` and `cartographer-develop-live` skills spawn per pull request,
`cartographer-security-reviewer`, the read-only attacker's-view pass they add on full-tier PRs,
`cartographer-source-reader`, the read-only reader of one voluminous source (the failed log of a
red CI run, a client's release notes) that returns a digest instead of the text, and the two a
maintainer spawns for the fixes of §Fixes without a plan: `cartographer-fix-implementer` and
`cartographer-advisory-fixer`.
Each pins a Claude model (`cartographer-source-reader` runs on Haiku 5.5, the others on Sonnet 5.5:
the plan writer drafts after the design decisions are taken, so its work is verification and pointers),
and the read-only ones (the reviewers, the plan writer and the source reader) carry `tools: Read, Grep, Glob, Bash`;
`TestClaudeSubagentsPinAModelAndReadOnlyOnesCannotEdit` checks both. The procedure itself stays in the skill (D327).

Working on more than one plan at a time? A subagent shares your working copy, so use a worktree per plan:

```bash
make worktree-add SLUG=my-change    # branches feat/my-change from upstream/main (origin/main without an upstream remote)
make worktree-rm  SLUG=my-change    # after the PR is merged; --force, so check first
make worktree-clean DRY=1           # or: list what a merged PR left behind (worktree, local and fork branch) ...
make worktree-clean                 # ... and remove it, each only after its PR is verified MERGED
```

## Adding a lint check

A check is declared once, in `internal/lint/registry.go` (D354); every table (who may accept it, whether `kb_repair` can fix it, whether the unattended repairs run it,
the Artifacts panel, what a write response evaluates) is derived from that entry, none is kept by hand.

**First decide it is a check** (D947). A lint finding is a problem: it has no severity, and the gate
passes only at zero. A check stays in the lint only if it has a definite fix, a state of the KB
that makes it disappear and is always better than the current one. If a reasonable maintainer could
look at the finding and decide "this is fine as it is", it is not a check but an *observation*
(`ObservationSpec` in `internal/lint/observations.go`): `kb_review` lists it as an item of kind
`observation`, with a fixture and a closing page state in `observations_test.go`, and no lint entry
point, write response or gate ever returns it. If it is not worth even that, do not add it.

Then three edits, nothing else:

1. **Register the spec**: a `CheckSpec` with its `Name`, `Level`, its
   reader's `Category` (D365: the Atlas Health panel lists every check by it; give the check a
   plain-words label in `web/src/lib/health.ts` too), and the flags that apply (`FixKinds`, `AutoRepairSafe`, `OnWrite`, `Judgement`…). A `LevelConcept` check
   is computed in `conceptFindings` (`internal/lint/conceptcheck.go`), which `lint` and the write path
   share; graph, map and artifact checks keep their dedicated pass and only register here.
2. **Emit through the constructor**: `newFinding("<name>", Finding{Path: …, Message: …})`. An
   unregistered name panics under test; a `Finding{…}` literal that sets
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
  goes through a PR with green CI (`make gate` locally first). In your clone,
  add the upstream remote once, so that worktrees branch from it and your fork
  (`origin`) only receives pushes:
  `git remote add upstream https://github.com/beppetemp/cartographer.git`.
- **Review costs more than writing**, so a PR is shaped to be cheap to review
  (D856). Three rules, checked by `scripts/pr-triage.sh`
  — run it on your branch before opening; the first two are also enforced by
  the required `plan-gate` check, which runs the same script on the PR, together
  with the next-train rule of §Releases (a PR that closes an issue of a later
  milestone waits):
  - a contributor's change over **150 production lines** (tests, docs and
    generated files excluded) — or one that changes a public contract (MCP tool
    schema, CLI flag, config key, on-disk format), which a reviewer applies, the
    script does not — closes a **plan issue the
    maintainer approved** — labelled `plan` and `approved` — **and assigned to
    you** (comment on the plan to claim it; the maintainer assigns it, so nobody
    ships against a plan someone else holds — or assigns your PR to you, since GitHub
    refuses to assign an issue to someone who never commented on it). The design is
    reviewed there, before any code exists; a trivial fix needs only `Closes #<n>`;
  - at most **500 production lines added** per PR: a larger change is split by
    work package into PRs that land in order. The exception is a maintainer's
    decision: the label `size-exception` on the PR (only triage rights set it;
    a maintainer's own PR may instead have a body line starting `Size exception:`
    that says why). A contributor's body line does not count;
  - the body follows `.github/pull_request_template.md`: its `Self-review`
    section reports an independent review run before opening — the
    `cartographer-contract-reviewer` subagent of the `cartographer-review-pr` skill, or your client's equivalent,
    at most three rounds — and its `Revert` section says what a revert of the
    released change would not undo. A `fix:` PR that repairs a regression also
    names the PR that introduced it, on a line `Regression-of: #<n>`: the
    reviewers are scored against such PRs (`cartographer-review-pr` §When a defect escapes).
  - **Docs (advisory)**: `pr-triage.sh` prints a `WARN` when a path mapped in
    `scripts/doc-owners.tsv` has at least **10 production lines** changed and its page (the
    one `docs/index.md` §Documentation maintenance rules names) is untouched. It does not
    block: either update the page, or put `Docs: none - <reason>` on a line of its own in
    the PR body (any author may; it silences the warning and the reviewer reads the reason).
  - **A new GitHub Action** is added pinned by its full commit SHA with the version in a
    trailing comment (`uses: owner/repo@<40 hex> # v1.2.3`; resolve it with
    `gh api repos/<owner>/<repo>/git/ref/tags/<tag>`). `make gate` fails on any other
    reference (D1020); Dependabot keeps the pins current.
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
    older spellings (`webui`, `mcpserver`, `skills`, `gitx`, …) group
    correctly in the history of `main`; a title may use only the scopes above.
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

## Releases

A release is a weekly **train**, not whatever has accumulated (D956). Times are Europe/Rome.

- **The train leaves on Monday at 08:30.** The `release-train` workflow merges the open
  release PR, and release-please then cuts the tag.
- **Freeze, Saturday 00:00 to Monday 08:30.** Nothing that adds or breaks behaviour
  lands: no `feat`, `perf` or `refactor`, and no breaking change (`!:` in the title or
  a `BREAKING CHANGE` footer). `fix`, `docs`, `test`, `ci`, `build` and `chore` may
  land, so Dependabot keeps working. In short: a PR merged by Friday ships the
  following Monday; a blocked one waits until after the release. A blocked commit on
  `main` during the freeze skips the train.
- **The release candidate.** On Saturday at 00:30 the workflow builds a server image
  `v<next>-rc.<YYYYMMDD>` (`preview.yml`) and comments the tag on the release PR. The
  image is started and smoked before it is published (tools, non-root user, a KB served
  over HTTP with a write that commits), so a candidate that exists starts. The same run
  renders the README film when its sources changed (`film.yml`, D1068): a red film job
  fails the candidate, so there is no `rc-ok` train that week. A maintainer
  runs it on a real deployment and adds the `rc-ok` label. Without the label, or if the
  candidate is not the current head of `main` (a fix landed after the build), the train
  is skipped with a comment (it says when the candidate build itself failed): run the
  candidate again by hand (`release-train`, `action: rc`), try it and add the label
  again, or wait a week.
- **Only the next train's work merges.** The next train is the open milestone due
  first. Work of a later milestone can be planned, implemented and reviewed at any
  time, but its PR waits, open, until the train before it has left: `plan-gate`
  fails a PR that closes an issue of a later milestone, and runs again by itself
  when the issue moves to another milestone or the milestones change (the maintainer
  closes the train's milestone after the release, moving what did not make it). A large change
  that spans trains lands as PRs that change nothing a user sees on their own.
- **No release with a known hole.** The train merges only when the security gate is
  clear: no open Dependabot alert, no open code scanning alert (a false positive is
  dismissed with its reason, not left open) and no draft security advisory whose fix
  PR is still open in its private fork. A report still without a fix does not block.
  The skip comment says only that the gate is not clear.
- **Out of train**, only for a regression of the latest release or a security fix: the
  maintainer merges the release PR by hand, with the same gate cleared.

## Issues: bug, plan (ideas go to Discussions)

An issue is the backlog. Open one as soon as something is worth coming back to,
from the template that fits:

- **Idea**: not an issue. Post it in
  [Discussions](https://github.com/beppetemp/cartographer/discussions) (category
  Ideas); blank issues are disabled. A maintainer turns an idea worth building
  into a plan.
- **Bug** (label `bug`): version, transport, what happened, a reproduction.
- **Plan** (label `plan`): a self-contained implementation plan, below.

The repository keeps only labels that a workflow or a skill reads, or that
change who acts:

| Label | Meaning |
|---|---|
| `bug`, `plan` | The two kinds of issue (ideas and questions are Discussions). An accepted issue without a plan yet sits in a milestone and is promoted to `plan` when it is not trivial |
| `approved` | A maintainer accepted the plan (only triage rights can set it); for a contributor's PR the plan is also assigned to them |
| `size-exception` | Maintainer-granted exception to the 500-line cap |
| `good first issue` | Small, self-contained, suitable for a first contribution: set at triage on the easiest issues of the next milestones, and dropped when nobody claims one by the Wednesday of its train week (the maintainer takes it or moves it to the next train) |
| `ci-red` | Opened by CI when `main` is red |
| `rc-ok` | Set by a maintainer after trying the release candidate: the Monday train merges the release PR only with it (§Releases) |
| `vuln-scan` | Created by `security.yml` when a vulnerability scan fails; never set by hand |
| `dependencies`, `autorelease: pending`, `autorelease: tagged` | Set by Dependabot and release-please, never by hand |

Milestones are the roadmap: one per weekly release, `vX.Y.0`; an issue in a
milestone is accepted work, and a PR sits in the milestone of the issues it closes
(the `pr-milestone` job of `plan-gate` sets it, and moves it with the issue), so a
train's milestone lists its PRs too. **Every accepted issue has a milestone**, approved plans
included: an open issue without one is not triaged yet, and the weekly triage puts
it in a train, moves it to Discussions or closes it. Keep two or three milestones
open: the non-trivial work of the next two is written as approved plans, ready to
be claimed; further out an issue stays a plain issue until its train comes closer,
because a plan's `file:line` pointers age.

When a bug is picked up for implementation, it is **promoted in place** (by a
maintainer: the label edit needs triage rights; a contributor opens a new Plan
issue that links the original): its body is rewritten from the Plan template and
it gets the `plan` label, so the discussion and the plan stay in one issue. A
trivial fix needs no plan: a PR that says `Closes #<n>` is enough. A plan is a
**proposal** until a maintainer adds the `approved` label, which only someone
with triage rights can set; no agent implements a plan without it, and none adds
it on its own initiative. A contributor claims an approved plan by commenting on
it; the maintainer assigns it to them, and `plan-gate` accepts only a plan
assigned to the PR author, or a PR the maintainer assigned to its author.

## Plan issues (design → implementation handoff)

Non-trivial changes start as a **plan issue** (proposed until a maintainer
adds `approved`): a GitHub issue created from the
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
in the `cartographer-develop-live` skill (`.claude/skills/cartographer-develop-live/SKILL.md`). What is specific
to it:

```bash
make worktree-add SLUG=my-change
cd .worktrees/my-change && make dev DATA=<data dir>   # or no DATA: the generated test KBs (D818)
```

`make dev` rebuilds and restarts the server on every Go change and serves the UI
with hot reload on `http://localhost:5173/ui/`. `DATA` is copied with its git
remotes keeping their fetch URL and an unreachable push URL, so nothing the session writes leaves the
machine; `API_PORT`/`UI_PORT` keep two sessions apart. With `DATA`, the instance also shows this machine's real client (Settings, Artifacts usage): `make dev` runs `cartographer sync --report-to` against it, which reports and changes nothing (D1044). The PR is opened as a
draft after the first accepted step, and its number is the decision number
(D672). With no plan issue, the PR is the record: its title (`feat:`/`fix:` for
what a user notices) and its body become the squashed commit, and so the
release changelog — the skill says what the body must hold. Everything else — `make gate`, docs and decision in the same PR, an
independent read of the diff before the merge — is the same as for a plan.

## Fixes without a plan (maintainers)

A `bug` whose fix is bounded skips the plan: the maintainer writes the design
decisions in a comment on the issue and puts it in a milestone, and a coordinator
spawns `cartographer-fix-implementer` on a worktree with the issue number. The same
agent takes a review round on a branch whose implementer is gone, with the verified
findings as its mandate. Everything else — tests that fail without the fix, docs in
the same PR, `make gate`, the review tier of `cartographer-review-pr` — is the same
as for a plan.

A defect found on the way — a reviewer's `pre-existing` finding, a flaky or broken
test, a doc that contradicts the code — does not wait in a backlog: it becomes a `bug`
issue in the open milestone, the next train, and when it is bounded the session that
found it fixes it the same day through this path. A rerun that turns a flaky test
green is not a fix. A security one goes to a draft advisory instead, and the next
paragraph applies.

A vulnerability in released code is fixed in the temporary private fork of its draft
advisory (`SECURITY.md`), by `cartographer-advisory-fixer`, and reviewed there with
`cartographer-security-reviewer`. Nothing about it is public until the release: the
commits state the new behaviour, not the hole. By Friday, before the freeze, the
maintainer lands the fix on the public repository on green CI and closes its PR in the
fork, so the Saturday candidate carries it and is tried with it; after the train has
released, the maintainer publishes the advisory.

## Reporting bugs

Open a GitHub issue from the **Bug** template: the version (`cartographer version`),
the transport in use (stdio or HTTP), and a minimal reproduction. For suspected security
issues, see [`SECURITY.md`](SECURITY.md) — please do **not** open a public
issue.
