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
make signoff  # maintainers only: gate (+ web legs), push the branch, attest the sha for CI
make build    # → bin/cartographer
make test     # go test ./...
make vet      # go vet ./..., also GOOS=windows
make fmt      # gofmt -w cmd internal
make smoke    # quick stdio smoke test
```

The deterministic E2E suite (`make e2e`) exercises the built HTTP server, CLI
client and temporary git remotes without model credentials; see
[`test/e2e/README.md`](test/e2e/README.md). CI runs `make gate` on a contributor's PR — the same
command, so there is one answer to "what must be green" — followed by
`make smoke-http`, `make smoke`, `make e2e` and `make test-install`; a maintainer's PR is
attested by `make signoff` instead (§Pull requests), which runs everything but `make test-install`
before the push (D1750).

The Atlas UI has its own loop (Node, outside `make gate`): `make web-test` for
the frontend tests, `make web` to rebuild the committed bundle, `make e2e-web`
for the browser suite — see [`docs/testing.md`](docs/testing.md) §Frontend. The
README animation needs nothing by hand: the release that changes `web/`
re-records it and the Surveyor-at-work animation (`make hero` and `make hero SPEC=surveyor` preview them locally), and the release candidate re-renders the
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
- The top level: `cmd/cartographer/` is the single binary (server, client, Surveyor, tray),
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

This repository is developed with two clients, **Claude Code** and **Kiro CLI** (D2323).
`AGENTS.md` is the canonical instruction file and `CLAUDE.md` is a one-line `@AGENTS.md`
import (an import rather than a symlink, because git does not materialise symlinks on a
Windows checkout without Developer Mode). Every directory with an `AGENTS.md` has
its own one-line `CLAUDE.md` import (D213). The skills are the directories of `.claude/skills/`
and the role bodies the files of `.claude/agents/`; the second client's definitions,
`.kiro/agents/<name>.json`, are generated from them (`make kiro-agents`), hold a pointer to the
body and are never edited. What differs between the two:

| Difference | Claude Code | Kiro CLI |
|---|---|---|
| Area `AGENTS.md` files | loaded on the first read of a file in the directory | not loaded unless the session starts at or below the directory: the mandate tells the role to read them |
| Role definitions | `.claude/agents/<name>.md` | `.kiro/agents/<name>.json`, generated, pointing at the same body |
| Skills | loaded from `.claude/skills/` | read as a procedure: the coordinator opens `.claude/skills/<name>/SKILL.md` |
| Where a session starts | the main clone or a worktree | the main clone, on `main`: workspace agents are found in the folder the session starts in and a resource path resolves against it, so the definitions read are `main`'s |
| Model of a subagent | the definition's `model` | the generated `model`; the coordinator's model is passed explicitly when the session starts, else the client's default is used |
| Sending a correction round | the same subagent is resumed | a subagent cannot be resumed: a new one gets the findings mandate; commits are added, never amended, on both |
| Read-only roles | no edit tool | no write tool; the shell remains on both, and the definition is what forbids a write |
| The check before reporting | an implementer: the targeted tests of the packages it touched (`go test ./<pkg> -run`, on the build host when there is one), never `make check-hosted`; the coordinator: `make check-hosted` on the tip | the same; a coordinator that works as a helper of another runs the local `make check` instead (the hosted check polls the API, whose hourly quota the account shares) |
| Who queues | a maintainer's coordinator, one lander at a time | the same; a coordinator that works as a helper of another pushes its branch and does not queue |
| Commit trailer | the client's `Co-Authored-By` | the client's `Co-Authored-By` |

The Kiro CLI column is the design of the generated definitions; the maintainer's probe of the
client (listed workspace agents, a read-only role spawned, the pinned model in the client's log)
is recorded here with its date and the client version when it is done.

A maintainer's agent iterates on the targeted tests of the packages it touched and never runs `make check-hosted`: the coordinator starts one (GitHub-hosted, in parallel with every other agent's, with a Windows leg) on the tip when it starts the review, never a check that waits for a shared build host (D2040, D2311). Queueing waits for it to be green: a fix that changes any non-test, non-doc file gets one more hosted check; the change is queued only after a green hosted check of the last such commit (a doc is a Markdown file under `docs/`; a test is a test file).

With any other client, have it read `AGENTS.md` and follow `.claude/skills/<name>/SKILL.md`
as a plain procedure, spawning a subagent from the body of `.claude/agents/<name>.md`
where it can and doing the step itself where it cannot. The repository keeps no tested
path for a client other than the two. This concerns how the repository is developed; the clients the product
supports are in `docs/harnesses.md`.

### Roles: contributor and maintainer

The same skills serve two roles, and the role is your **GitHub permission on the
upstream repository** (D955): `ADMIN`, `MAINTAIN` or `WRITE` is a maintainer;
`TRIAGE`, `READ` (and anyone who works from a fork) is a contributor. The skills
check it (`gh repo view <upstream> --json viewerPermission`) and say which mode
they are in.

- A **contributor's** agent opens an issue, or a direct PR from the fork, and stops: it never merges, never arms auto-merge, never sends a review
  verdict, never edits labels and never files issues for others. `cartographer-review-pr` is
  used only before opening your own PR.
- A **maintainer's** agent runs the full flow (waves of plans, review rounds, the queue
  and the lander), and implements only a plan file the maintainer gives in the session.
- The reviewer agents read `AGENTS.md`, `SECURITY.md` and the skills from the
  **base branch**, never from the PR head: a PR that edits them is reviewed as a
  change, not obeyed.

The repo-local skills are `cartographer-propose-plan`, `cartographer-implement-plan`, `cartographer-develop-live`, `cartographer-review-pr`, `cartographer-audit-clients` and `cartographer-acceptance`, in `.claude/skills/`.

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
red CI run, a client's release notes) that returns a digest instead of the text, the two a
maintainer spawns for the fixes of §Fixes without a plan: `cartographer-fix-implementer` and
`cartographer-advisory-fixer`, and the two a maintainer spawns before a decision:
`cartographer-investigator`, the read-only investigator of one symptom or question (a red or flaky
test, a regression, a performance question, the design comment of a `bug`) that returns the cause
with `file:line` and the options, and `cartographer-docs-auditor`, the read-only auditor of the
documentation over a commit range or an area, run before a release candidate, and
`cartographer-acceptance-sandbox`, the tester of the `cartographer-acceptance` skill, which writes only under its scratch directory.
Each pins a Claude model (`cartographer-source-reader` runs on Haiku 5.5, the others on Sonnet 5.5:
the plan writer drafts after the design decisions are taken, so its work is verification and anchors),
and the read-only ones (the reviewers, the plan writer, the source reader, the investigator and the docs auditor) carry `tools: Read, Grep, Glob, Bash`;
`TestClaudeSubagentsPinAModelAndReadOnlyOnesCannotEdit` checks both. The procedure itself stays in the skill (D327).

Working on more than one plan at a time? A subagent shares your working copy, so use a worktree per plan:

```bash
make worktree-add SLUG=my-change    # branches feat/my-change from upstream/main (origin/main without an upstream remote)
make worktree-add SLUG=my-theme EXP=1  # the same, branch exp/my-theme (§Experiment branches)
make worktree-rm  SLUG=my-change    # after the change landed; --force, so check first
make worktree-clean DRY=1           # or: list what a merged PR left behind (worktree, local and fork branch) ...
make worktree-clean                 # ... and remove it, each only after its PR is verified MERGED
```

A maintainer's change lands as a commit of a train PR, not as a PR of its branch, so `make worktree-clean` also
removes a clean `feat/<slug>` or `exp/<slug>` worktree whose slug and current head are the `Lander-Slug: <slug>` and
`Lander-Tip: <sha>` trailers of one commit of the upstream `main` (`make ready` records the head it squashed, the lander
keeps both on the landed commit, a copy in the message is dropped) once `ready/<slug>` is gone from the upstream (a
failing `ls-remote` counts as still there); a reused slug, or a branch with a commit after the queued one, is kept;
worktrees landed before the trailer existed are removed by hand. A lander runs the scripts of its own checkout of
`origin/main`, so removing the worktree it was started from is harmless; `make worktree-rm` and `make worktree-clean`
refuse only the worktree a lander run with `LAND_SELF=1` runs its scripts from (`scripts/lander-guard.sh`): removing it
deletes the scripts under the lander, whose next gate would fail; wait for it to exit (`make land-status`).

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
  (D856). Two rules, checked by `scripts/pr-triage.sh`
  — run it on your branch before opening; the size cap is also enforced by
  the required `plan-gate` check, which runs the same script on the PR. A
  contributor needs no plan and no assignment: explain the why in the PR, and
  expect a wrong design to be closed in review, so ask in an issue first when in doubt:
  - at most **500 production lines added** per PR (tests, docs and generated files excluded): a larger change is split by
    work package into PRs that land in order. The exception is a maintainer's
    decision: the label `size-exception` on the PR (only triage rights set it;
    a maintainer's own PR may instead have a body line starting `Size exception:`
    that says why). A contributor's body line does not count. A production file whose lines cannot be counted
    (a binary file, or one a `.gitattributes` marks `-diff`) counts as over the cap, so it needs that exception too;
  - the body follows `.github/pull_request_template.md`: its `Self-review`
    section reports an independent review run before opening — the
    `cartographer-contract-reviewer` subagent of the `cartographer-review-pr` skill, or your client's equivalent,
    at most three rounds — and its `Revert` section says what a revert of the
    released change would not undo. A contributor's `fix:` PR that repairs a regression also
    names the PR that introduced it, on a line `Regression-of: #<n>` (a maintainer's queued
    `fix` writes `Introduced-by:`, below): the reviewers are scored against such changes
    (`cartographer-review-pr` §When a defect escapes).
  - **Docs (advisory)**: `pr-triage.sh` prints a `WARN` when a path mapped in
    `scripts/doc-owners.tsv` has at least **10 production lines** changed and its page (the
    one `docs/index.md` §Documentation maintenance rules names) is untouched. It does not
    block: either update the page, or put `Docs: none - <reason>` on a line of its own in
    the PR body (any author may; it silences the warning and the reviewer reads the reason).
  - **A new GitHub Action** is added pinned by its full commit SHA with the version in a
    trailing comment (`uses: owner/repo@<40 hex> # v1.2.3`; resolve it with
    `gh api repos/<owner>/<repo>/git/ref/tags/<tag>`). `make gate` fails on any other
    reference (D1020); Dependabot keeps the pins current.
- **The fast lane** (D2315): a maintainer's change of 51 to 150 production lines that matches no path row with a tier
  floor in `scripts/profiles.tsv` (so no invariant area and no supply-chain file: nothing under `scripts/`, `.claude/`
  or `.github/`, no Makefile, installer, `.gitattributes`, `test/quarantine.tsv` or embedded Surveyor prompt), touches
  nothing under `docs/decisions/` and holds no binary file and no gitlink. `pr-triage.sh` prints `Tier: fast` from
  paths, size, role and the kind of entry only (a rename is read by both paths); the review is one contract-reviewer
  round with `make check-hosted` beside it, its lows fixed in one pass and the triage run again on the result, which
  takes a change that no longer qualifies back to the rounds of its tier. `make ready` requires `REVIEW="<tier>, <n> rounds"`, and the lander re-runs main's triage on every queued
  change and rejects one with no record or one recorded below its computed tier. The pre-check and the train gate are unchanged.
- **What runs where** (D1592, D1610): a contributor's PR runs `test`, the web legs when the frontend changed, `pr-title`
  and `plan-gate`; a maintainer's PR is pushed with `make signoff`, whose `signoff` status `gate` looks for; a train is
  gated once and `main` runs its jobs again on the push. The required checks are `gate`, `pr-title` and `plan-gate`.
  The train gate is hosted by default (D2040, D2293); `make land GATE=host` gates on the build host.
  A lander whose `scripts/land.sh` or `scripts/signoff.sh` differs from `origin/main`'s exits at its start and before each train (`LAND_ALLOW_STALE=1` runs anyway; `make land-status` only warns): run it from an up-to-date tree.
  `make check` (affected packages) is the tier to iterate with and `make check-hosted` (on GitHub, with a Windows leg) the coordinator's, once on the tip beside the review.
  Which job runs on which event, and what each gate runs, is the generated table of `docs/testing.md` §Levels.
- **A maintainer's change lands in a train** (D1706, D2304): finished and reviewed on its
  branch, it is queued with `make ready`, and the lander lands it with the changes it is chained to.
  - **Queueing.** `make ready MSG=<message file> REVIEW="<tier>, <n> rounds"` queues it as **one commit** whose message
    is the changelog entry (a conventional title, body lines unwrapped, `Closes #n`; checked as
    `pr-title` and `plan-gate` would check a PR, `Size exception: <why>` included).
    `AFTER=<slug>` lands it after another queued change (a branch built on that change's
    branch is queued with its own diff only, also once that change has landed; a slug neither queued
    nor landed is refused), `HOLD=1` keeps it queued. `REVIEW=` is required: it records the review, `make ready` refuses a record below the tier its triage computes, and the lander checks it again against the tier main's triage computes.
    `USAGE="pass, <n> entries"` (or `USAGE="waived: <reason>"`) is required when the triage prints `Usage-test: required`
    (a change to the CLI, a client, provisioning, an installer or a bundled skill): it records that the change was tried on
    its built binary (`docs/testing.md` §Usage test), `make ready` refuses without it, and the lander rejects a queued change that lacks it.
  - **A chain is one unit.** Changes linked by `AFTER=` (a plan's chain) are one unit, a change
    with no link a unit of its own: a unit lands whole, in one train, or not at all, and its verdict is
    the pre-check of its tip, the change no other follows. A held member,
    or during the release freeze a `feat`, `perf`, `refactor` or breaking one, holds the whole chain; a
    rejected member blocks the others, which stay queued until it is queued again.
  - **The train.** The lander (`scripts/land.sh`, started by `make ready`, stopping when
    nothing is landable) cherry-picks whole units onto `main`, up to 8 changes, numbers their pending
    decisions, gates the train **once** with `make signoff` (the package of a named Go test that fails
    runs once more inside it, and a pass is filed as flaky; red twice, or red on that retry: bisection over
    whole units rejects the one that breaks it, its tip to `failed/<slug>`, whose last commit message says
    why), and opens one PR `chore(dev): train <id>, <k> change(s)`, **rebase-merged** so each change stays
    its own commit on `main`.
  - **The merge.** The lander merges it itself, once its required checks pass and only while
    `main` is still the commit the train was gated on (`main` does not require a branch to be
    up to date): if `main` moved, during the gate or before the merge, the PR is closed and the
    train composed and gated again. A required check that fails is rerun once before the
    lander stops (one in `startup_failure` stops it at once: it will not turn green), and an open train
    PR on the current `main` that it did not open is adopted (one on an older `main` is closed).
  - **Pre-check and status.** Each queued change gets a hosted pre-check
    (`ready-check.yml`: its Windows tests and, for `web/`, the frontend unit tests, on its chain,
    the changes it follows through `AFTER=`, replayed on the current `main`): a train of
    several units takes only those whose tip is green, and a red one is gated alone. The lander waits up to
    10 minutes (or for 4 green changes) before composing so that changes queued meanwhile share a train
    (`make ready NOW=1` skips the wait). `make land-status` shows the queue by unit with each pre-check
    verdict and a red one's failed jobs; `make requeue SLUG=<slug>` rebuilds a queued or rejected change and the ones after it on the current `main`, keeping messages and queue places, in one atomic push (`FIRST=1`: first in the queue); `make land` runs the lander in the foreground (`DRY=1` prints the next train).
    Queued changes have no cap: a train holds at most 8 changes, and a longer chain is gated alone. On macOS the
    lander keeps the machine from idle sleep while it runs (`caffeinate -i`); a closed lid still stops it.
  - **The network.** A failed fetch of origin is tried again (`LAND_INFRA_TRIES`, `LAND_INFRA_WAIT`) before the
    lander stops, every `gh` call is bounded (`LAND_GH_TIMEOUT`), and a release listing it cannot read keeps the last
    freeze state it read: while that state holds a frozen unit and nothing else is landable, the lander waits for the
    listing instead of stopping. The running lander writes its state to `status.json` in its state directory at each
    transition (phase, train, run, window, the units not landable and why, the last rejection; `scripts/land.sh` header).
- **No open PR depends on another open PR**: a contributor's dependent work goes in the same
  PR, or waits for the merge. A maintainer's dependent change is queued with `AFTER=<slug>`
  and lands with that change, in one train.
- **A maintainer's queued `fix` says where its defect was found and what introduced it**, on
  lines of its message that start at column 0, anywhere in it (they need not be trailers:
  a `Closes #n` line beside them is fine). One `Found-by: <value>` line per defect it fixes,
  the value one of `scripts/found-by.tsv` (`scripts/trailers-lint.sh --values` prints them),
  and one `Introduced-by: <ref>` line per change that introduced one: a commit of `main`,
  the slug of a landed change (its `Lander-Slug`), `#<n>` for a pull request, or `unknown`
  alone when it cannot be found (a `Regression-of: #<n>` line counts as one). Any queued
  change may also carry one `Cost: impl <n>k, review <n>k, rounds <n>` line, which the
  coordinator writes: thousands of tokens of the implementing and the reviewing subagents,
  and the review rounds, `?` for a figure the client does not show. `make ready` refuses a
  `fix` without the two lines, a ref that names no change of `main` and a `Cost:` line of
  any other shape (`scripts/trailers-lint.sh`); none of these lines reaches the changelog.
  A contributor's PR needs none of them.
- PRs are **squash-merged** (a train is rebase-merged, its commits already are the
  changes) and the PR title becomes the commit message on
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
  re-propose) gets **one** decision file. A maintainer's change writes it as pending,
  `make decisions-new SLUG=<slug> TOPIC=<topic>` → `docs/decisions/D-new-<slug>.md`, and cites
  it as `D-new-<slug>` (a link as `D-new-<slug>.md`): the lander gives it the next free number and rewrites the citations
  in the change's own files (D1706); a citation or an edit of one whose change already landed
  follows the number it was given, and a second `D-new-<slug>` for a landed slug is rejected. A contributor's PR numbers it after the PR (D672):
  `make decisions-new N=<n> SLUG=<slug> TOPIC=<topic>`.
  A record that replaces or changes an older one declares it in both frontmatters
  (`supersedes`/`superseded_by`, `amends`/`amended_by`: `docs/decisions.md` §Adding a decision).
  Smaller choices need no file — the PR is
  their record. Do not regenerate `docs/decisions.md` in a feature PR: the
  release PR regenerates the index (D328).
- New MCP tools follow the checklist in
  [`internal/mcpserver/AGENTS.md`](internal/mcpserver/AGENTS.md) §Adding a tool
  and come with tests in `internal/mcpserver/server_test.go`.
- Coding conventions: [`docs/conventions.md`](docs/conventions.md).

## Releases

A release happens when the maintainer decides (D1384, superseding the weekly calendar
of D956, D989 and D1192). There is no schedule: the `release-train` workflow is
dispatched by hand with one of three actions.

- **The release.** `action: rc` builds the candidate, `action: release` merges the open
  release PR with every check below, `action: discard` deletes the candidate(s) of the
  pending version. After the merge release-please cuts the tag and creates the release
  as a draft: `release.yml` publishes it only after the binaries and the versioned images exist, then pushes the
  Homebrew cask and moves `:latest`, so no client is told of a version it cannot install (D1350).
  The release page shows the grouped `CHANGELOG.md` section of the tag, set by that same step.
  The release page and the release PR show the same compact notes, built from that section
  (`gen -compact`): one bullet per commit (a commit release-please lists several times is
  one bullet), short SHAs and `#N` as plain text that GitHub links itself, breaking changes
  first and then each area's Features, Bug Fixes and Performance Improvements; `CHANGELOG.md` keeps the full links.
  The PR body holds them in release-please's own header and footer so it can still parse the
  body on merge. Notes over 60000 characters (`gen -split`) go in comments marked
  `<!-- release-notes-part:N -->` after the first part, which `release-pr` keeps in step and
  deletes once the notes fit (release-please replaces a body over GitHub's 65536 characters
  with a link).
- **Freeze, opened by the candidate.** It is open from the moment a candidate tag
  `vX.Y.Z-rc.<YYYYMMDD>.<n>` with `X.Y.Z` greater than the latest stable release exists, until
  that version is released (`release.yml` deletes its candidates) or the candidate is
  discarded. `scripts/freeze-open.sh` answers it from the releases listing (never
  `releases/latest`, which hides a 0.x pre-release; a draft is not released yet; an
  unreadable listing counts as open, and `--candidate(s)` then exit 3, which `discard`
  fails on instead of reporting nothing to discard). While it is open nothing that adds or breaks
  behaviour lands: no `feat`, `perf` or `refactor`, and no breaking change (`!:` in the
  title or a `BREAKING CHANGE` footer). `fix`, `docs`, `test`, `ci`, `build` and `chore`
  may land, so Dependabot keeps working, but its auto-merge is not armed while the freeze
  is open (D1180): `dependabot-arm` follows `freeze-open.sh`, disarming what was armed
  when a candidate is cut and arming the open bumps after the release or a `discard`. A
  blocked commit on `main` since the oldest candidate of the pending version skips the
  release. A PR the freeze blocks waits until the release.
- **The release candidate.** `action: rc` builds the candidate
  `v<next>-rc.<YYYYMMDD>.<n>` (`preview.yml`; `<n>` counts the day's candidates from 1, so every candidate has a tag of its own, and the older `v<next>-rc.<YYYYMMDD>` shape is still read) and comments the tag on the release PR: one comment, edited for each new candidate (the latest tag, the earlier ones in a short list). It is a
  server image, a Surveyor image **and** a GitHub pre-release with the binaries (the same archives and
  `sha256sums.txt` as a stable release). Both images are started and smoked before anything
  is published (tools, non-root user, a KB served over HTTP with a write that commits for the server;
  tools present and absent, entrypoint for the Surveyor),
  so a candidate that exists starts; the binaries are published only after the images,
  and only once the built binary prints the candidate's tag. The same run renders the
  README film when its sources changed (`film.yml`, D1068): a red film job fails the
  candidate, so there is nothing to release until it is fixed. `scripts/ac-report.sh` lists the
  acceptance criteria landed since the last stable release (§Plans), `manual` rows first: they are
  the input of the candidate's test matrix. A maintainer runs the images on a
  real deployment and the binaries as native service and client (below), adds the
  `rc-ok` label and runs `action: release`. Without the label, or if the candidate
  is not the current head of `main` (a fix landed after the build), the release is
  skipped with a comment (it says when the candidate build itself failed): run the
  candidate again by hand (`action: rc`), try it and add the label again. An
  `action: rc` always builds, into a new tag (the oldest candidate, from which the freeze check
  starts, is never replaced). A pull request
  touching `film/` runs `film-check` (30 frames, the runner's tools), so the encode chain
  fails there and not in the candidate.
- **Trying the candidate as native service and client.** On macOS or Linux,
  `curl -fsSL https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.sh | CARTOGRAPHER_INSTALL_TAG=v<next>-rc.<YYYYMMDD>.<n> sh`
  (Windows: `$env:CARTOGRAPHER_INSTALL_TAG='v<next>-rc.<YYYYMMDD>.<n>'` before
  `install.ps1`) installs that tag, checksum-verified like any release, over a stable
  install; the next `cartographer sync` replaces the native service with it (D199) and
  its schema migrations run as a stable upgrade's would. Back to stable: `install.sh`
  with `update` and without the variable installs the newest **stable** release (a
  downgrade while `v<next>` is not out), or `brew reinstall --cask cartographer` for a
  Homebrew install; after the release, the same `update`. Schema migrations are not
  reverted: going back past one is not supported. A candidate never appears in
  `cartographer update check`, in `kb_status` `latest_version`, in the Atlas history,
  in the installers' default choice or as the Homebrew cask (every tag with a semver
  pre-release suffix is skipped, whatever GitHub's pre-release flag says), and the
  stable release deletes its version's candidates (`release.yml`, `candidate-cleanup`).
- **The version comes from the commits.** release-please computes it from the
  conventional commit types (D1453): `feat` a minor, `fix` a patch, a breaking change a
  minor while the project is below 1.0. No milestone names a release. A larger bump than
  the commits give is a commit with the footer `Release-As: x.y.z`, added by hand. A large
  change still lands as PRs that change nothing a user sees on their own.
- **No release with a known hole.** The release merges only when the security gate is
  clear: no open Dependabot alert, no open code scanning alert (a false positive is
  dismissed with its reason, not left open) and no draft security advisory whose fix
  PR is still open in its private fork. A report still without a fix does not block.
  The skip comment says only that the gate is not clear; when a read of the gate fails
  (an expired or under-scoped token), the log names the read's number and HTTP status.
- **Out of band**, only for a regression of the latest release or a security fix: the
  maintainer merges the release PR by hand, with the same gate cleared.
- **Maintenance release**, for emergencies only (a feature never needs it: it lands in
  changes that show nothing until the last, §Experiment branches), `x.y.(z+1)` from a branch `release-<x.y>` (D1443), when
  `main` already holds work for the next minor that should not ship yet. The
  maintainer cuts the branch from the `vx.y.z` tag, cherry-picks with `-x` what ships,
  and the branch carries `release-please.yml` (a push runs the pushed branch's copy):
  release-please opens `release-please--branches--release-<x.y>` against it. The branch's
  `release-please-config.json` sets `pull-request-title-pattern` to
  `chore: release${component} ${version}`: `pr-title` knows no branch scope. An empty
  commit with the footer `Release-As: x.y.(z+1)` names the version, since a `feat`
  would make it propose the next minor. A candidate is `preview.yml` dispatched on the
  branch (`--ref release-<x.y>`, tag `vx.y.(z+1)-rc.<YYYYMMDD>`): it opens the freeze
  on `main` too, until the release deletes it. The release PR is merged by hand with
  the security gate above cleared (`release-train` serves `main` only); the tag runs
  `release.yml` as usual. Only while it is the highest version: the release moves the
  image's `latest` and the Homebrew cask, so a branch older than the latest stable
  release gets no release this way. `main` keeps its own changelog from `vx.y.z`, so the
  next minor lists the backported entries again.
- **Process week.** After a release is published, which closes the freeze, the open
  `process` issues (§Issues) are worked before feature plans start. Their input is the
  escape reports since the previous release, the comments of the `Escape report` issue. A
  process follow-up found during a release is filed as a `process` issue at once, never
  left as prose.
- **Trying the candidate on the lanes that need no private machine.** The
  `cartographer-acceptance` skill holds the procedure: a test matrix built from the commits and
  the closed bugs since the previous stable release, and three lanes any maintainer's session can run
  with the repository alone: static readiness (`cartographer-docs-auditor` over the range and the
  checks of `docs/testing.md` §Before a release), an all-in-one instance in an isolated HOME tried by
  `cartographer-acceptance-sandbox`, and a usage test with a headless agent. A finding is a `bug`
  issue; a new round is a new candidate. Lanes on a maintainer's own machines are outside the repository. The scripted lanes (the battery and the upgrade) also run on `main` every night (`nightly.yml`): a red night opens the `nightly-red` issue.

## Experiment branches (`exp/*`)

One lane (D1707): every maintainer change, a bug fix or a slice of a feature,
reaches `main` through the queue (`make ready`, §Pull requests), each small enough for the
size cap. A feature that is not ready for users lands in changes that show nothing on
their own, the last one turning it on, so `main` stays releasable without a
`release-x.y` branch. An `exp/<theme>` branch exists only for a **public build** of work
that is not on `main`: an image or binaries to try on a real deployment.

- **Create it** from `main`: `make worktree-add SLUG=<theme> EXP=1` (branch `exp/<theme>`,
  worktree `.worktrees/<theme>`). `make worktree-rm SLUG=<theme>` removes both (it reads the branch from the worktree).
- **Stay aligned**: merge `main` into the branch before every public build, so what is
  tried is what would land.
- **CI without a PR**: `gh workflow run ci.yml --ref exp/<theme>` runs every job
  (`workflow_dispatch`); pushes to the branch do not run CI by themselves.
- **The build** is a pre-release tagged `vX.Y.Z-exp.<YYYYMMDD>` (`gh workflow run preview.yml --ref exp/<theme> -f tag=vX.Y.Z-exp.<YYYYMMDD>`; the tag is a required input).
  `release.yml` ignores it and the candidate cleanup only deletes `-rc.` tags, so the
  maintainer deletes the pre-release and its tag by hand when the trial is over.
- **Landing**: the branch is never merged; its work is queued as changes made off `main`,
  each with its own pending decision file. The branch is deleted when the last has landed.

## Issues: bug, idea

An issue is the backlog. Open one as soon as something is worth coming back to,
from the template that fits:

- **Bug** (label `bug`): version, transport, what happened, a reproduction. A
  question the [FAQ](docs/faq.md) and the docs do not answer is a documentation bug.
- **Idea** (label `idea`): what should be possible and why. It is a proposal, not
  accepted work: at triage a maintainer adds `accepted` or closes it with the
  reason. Blank issues are disabled.
- An agent can draft a bug or idea report with the bundled `cartographer-feedback`
  skill: it files nothing and prints a prefilled URL for you to submit;
  `cartographer feedback` removes every real name before you do.
- Contributors write no plans: they open a bug, an idea or a direct PR.

The repository keeps only labels that a workflow or a skill reads, or that
change who acts:

| Label | Meaning |
|---|---|
| `bug`, `idea` | The kinds of issue |
| `accepted` | Triaged and accepted. Not yet started while it has no assignee; assigned to a maintainer once work starts, so a contributor sees it is taken |
| `plan`, `approved` | Mark the closed plan issues of the former process; no longer set |
| `size-exception` | Maintainer-granted exception to the 500-line cap |
| `good first issue` | Small, self-contained, suitable for a first contribution: set at triage on the easiest accepted issues, and dropped when nobody takes one for a while |
| `ci-red` | Opened by CI when `main` is red |
| `nightly-red` | Opened by `nightly.yml` when a lane of the nightly acceptance run is red, closed by the next green night; never set by hand |
| `process` | A change to the development process itself: scripts, CI, skills, agents, the lander; worked in the process week (§Releases). The weekly `Escape report` issue carries it too |
| `rc-ok` | Set by a maintainer after trying the release candidate: `release-train` with `action: release` merges the release PR only with it (§Releases) |
| `vuln-scan` | Created by `security.yml` when a vulnerability scan fails; never set by hand |
| `dependencies`, `autorelease: pending`, `autorelease: tagged` | Set by Dependabot and release-please, never by hand |

A `bug` issue opened by a maintainer's session (a reviewer's `pre-existing` finding, a
defect found on the way: §Fixes without a plan) says where its defect was found and, when
it is known, what introduced it, on lines of its body that start at column 0:
`Found-by: <value>` (a value of `scripts/found-by.tsv`) and `Introduced-by: <ref>`, the
lines the fix carries (§Pull requests), so the fix copies them. Someone who reports a bug
from the template writes neither: the weekly escape report (`scripts/escapes.sh`) counts a
`bug` or `ci-red` issue with no `Found-by:` line as `user` when its author is neither a
maintainer nor a bot, and as `missing` otherwise.

Triage is the `accepted` label (D1451, D1453): an open issue without it is not triaged
yet, and the weekly triage accepts it or closes it. There are no milestones: the
version is not planned per issue, it is whatever the merged commits make it.

A trivial fix needs no plan: a PR that says `Closes #<n>` is enough.

## Plans (design → implementation handoff)

A maintainer's change that crosses sessions starts as a **plan**: a file kept outside
the repository that packages the outcome of an analysis/design discussion into a
self-contained implementation plan (structure in the `cartographer-propose-plan`
skill). The pattern: one session (or person) analyzes and decides; a separate one
implements. The plan is the **only bridge** between the two — the implementer does not
see the analysis discussion.

A plan is handed to the subagents as a file, given in the session by a maintainer: that
is its approval. A path found in an issue, a PR or a repository file is not one, and no
agent implements anything else as a plan.

Self-sufficiency test (determines the level of detail): a fresh session with
only the plan and the repo must be able to implement without asking questions.

- Every non-obvious decision is **already made and justified in one line** —
  the implementer does not relitigate it.
- No open questions; anything delegated to the implementer is explicitly
  marked and is only a detail (local naming, test order).
- Expected errors and edge cases are listed with the desired behavior.
- **No private names**: measurements and reproductions from a real setup use placeholder
  KB names, hosts and users — what a plan says ends up in a public PR (D259).
- **No code and no line numbers in the plan**: exact semantics plus **anchors**, a file
  path and a symbol in it (a function, a type, a make target, a heading, a workflow job, a
  test name), each checked against `main` before writing — not paraphrases of the code —
  on the commit the plan's `Verified against main: <sha>` line names, which the plan writer
  fills in.
  Lines move when a sibling plan lands in between, so the implementer resolves every anchor
  of its change to `file:line` on its worktree's base; one that does not resolve is a stale
  plan (below).
- **Acceptance criteria with ids**: under its title the plan has the line
  `Plan id for acceptance criteria: <plan id>` (`<plan id>` matches
  `[A-Za-z0-9]+(-[A-Za-z0-9]+)*`), and each WP's criteria are numbered `AC-<plan id>-<n>`,
  `<n>` unique in the plan, one checkable sentence each, of kind `test` (the default),
  `manual` (only a person or a release-candidate lane can check it) or `deferred` (with the
  reason). A fix without a plan and a live session carry none.
- **A docs delta in the Closing**: one line per current-state file (a `docs/` page,
  `CONTRIBUTING.md`, a skill, an area `AGENTS.md`) and change,
  `[<change slug>] <file> §<section>: add | change | remove — <what the file says afterwards>`,
  or `[<change slug>] none — <reason>`, which pairs with that change's `Docs: none - <reason>`
  message line (§Pull requests); a line without a slug belongs to the plan's last change.
- A plan has a `## Changes` section that groups its work packages into the fewest changes
  the size cap allows (a change under about 150 production lines is merged into a neighbouring
  change of the plan unless a code dependency or a web-bundle conflict forbids it) (each change
  is one queued commit, §Pull requests), by file-set and
  dependency, with for each change its slug, its commit title, its WPs, the estimated
  production lines and the change it is queued after, if any; each WP is a body line
  `type(scope): ...` (one unwrapped line, §Pull requests body-line rule) so the changelog
  keeps one entry per WP, omitted when it equals the commit title (a second entry for one
  commit, #1670); `cartographer-plan-writer` reports it.
- The changes that depend on each other form one **chain**: each keeps its own branch and
  worktree (`make ready` takes the slug from the branch and squashes it), stacked on its
  predecessor's, the worktree of change n+1 created from `feat/<n>` once change n is committed
  (only the coordinator creates worktrees, and sends the path to the same implementer). One implementer
  may write them in order, continuing in each new worktree, with a commit series and a message file per change;
  the chain is reviewed and checked once on its tip and queued in order with `AFTER=`; a review
  fix lands on the branch of the change it concerns and its successors are rebased
  (`git rebase --onto`).
- A single analysis may yield **several plans**: each one states the
  cross-plan execution order and which sibling plans touch the same files
  (those are built one on the other and queued in order, `AFTER=<slug>`).

The change's commit message explains the why, since the plan stays private: a reader sees
the commit, the train PR that landed it and its decision file, nothing else. No script
reads the plan: what a script checks travels in the message. Its trailer block (the last
paragraph, after a blank line) has one line per acceptance criterion of the change's WPs,
`AC: <criterion id> <kind> <the sentence>` at column 0, and the test that proves a `test`
criterion carries its id in a comment on or right above it. The implementer executes
the work packages in order (`make check` green after each; the train's gate is the full
one), updates the docs per the lines of the plan's docs delta that name its change, which the
contract reviewer checks line by line, and, when the Closing names one,
adds the decision file as pending, `docs/decisions/D-new-<slug>.md`, numbered when it lands
(D1706; the index is regenerated by the release PR, D328). The file records the
implemented choice and any deviation from the plan; it does not duplicate current-state
documentation. If the plan contradicts the
actual code, or an anchor does not resolve, stop and tell the maintainer: the plan may be
stale relative to `main`.

## Live sessions (no plan)

When the outcome is found by trying it — the Atlas UI, a UX detail, CLI output to
tune — design and implementation stay in one session, with the user trying each
step on a running instance. The procedure, the roles and the subagent mandate are
in the `cartographer-develop-live` skill (`.claude/skills/cartographer-develop-live/SKILL.md`). What is specific
to it:

```bash
make worktree-add SLUG=my-change
cd .worktrees/my-change && make dev DATA=<data dir>   # or no DATA: the generated test KBs (D818)
```

`make dev` rebuilds and restarts Meridian on every Go change and serves the UI
with hot reload on `http://localhost:5173/ui/`. `DATA` is copied with its git
remotes keeping their fetch URL and an unreachable push URL, so nothing the session writes leaves the
machine; `API_PORT`/`UI_PORT` keep two sessions apart; `TESTKBS=0` with `DATA` leaves the generated test KBs (and their synthetic Surveyor) out, to judge the Atlas on real data only. With `DATA`, the instance also shows this machine's real client (Settings, Artifacts usage): `make dev` runs `cartographer sync --report-to` against it, which reports and changes nothing (D1044). Nothing is pushed
while iterating; at the end the change is queued with `make ready`. With no plan, the
commit message is the record: its title (`feat:`/`fix:` for what a user notices) and its
body become the commit on `main`, and so the release changelog — the skill says what the
body must hold. Everything else — docs and decision in the same change, an independent
read of the diff before it is queued — is the same as for a plan.

## Fixes without a plan (maintainers)

A `bug` whose fix is bounded skips the plan: the maintainer writes the design
decisions in a comment on the issue and adds `accepted`, and a coordinator
spawns `cartographer-fix-implementer` on a worktree with the issue number. When the
cause or the options are not known yet, `cartographer-investigator` finds them first and
drafts that comment; the maintainer decides and posts it. The same
agent takes a review round on a branch whose implementer is gone, with the verified
findings as its mandate. Everything else — tests that fail without the fix, docs in
the same change, `make check`, the review tier of `cartographer-review-pr`, `make ready` —
is the same as for a plan.

A defect found on the way — a reviewer's `pre-existing` finding, a flaky or broken
test, a doc that contradicts the code — does not wait in a backlog: it becomes an accepted
`bug` issue with its `Found-by:` line, and its `Introduced-by:` line when the session knows
the change (§Issues), and when it is bounded the session that
found it fixes it the same day through this path. A flaky or broken test met on the way —
in a train gate, in the pre-check of `make ready`, or in a coordinator's
own `make check-hosted` run — is `Found-by: gate`; `agent` is for a defect found by reading
or using the code while working on something else. A rerun that turns a flaky test
green is not a fix. A security one goes to a draft advisory instead, and the next
paragraph applies.

A vulnerability in released code is fixed in the temporary private fork of its draft
advisory (`SECURITY.md`), by `cartographer-advisory-fixer`, and reviewed there with
`cartographer-security-reviewer`. Nothing about it is public until the release: the
commits state the new behaviour, not the hole. Before the maintainer cuts the
candidate, the maintainer lands the fix on the public repository on green CI and closes
its PR in the fork, so the candidate carries it and is tried with it; after the release,
the maintainer publishes the advisory.

## Reporting bugs

Open a GitHub issue from the **Bug** template: the version (`cartographer version`),
the transport in use (stdio or HTTP), and a minimal reproduction. For suspected security
issues, see [`SECURITY.md`](SECURITY.md) — please do **not** open a public
issue.
