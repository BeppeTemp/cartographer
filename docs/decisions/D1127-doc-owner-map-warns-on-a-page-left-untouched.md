---
topic: project-governance
---

# D1127 — A doc-owner map warns when a page is left untouched

**Decision.** `scripts/doc-owners.tsv` maps path globs to the docs page that
describes them, mirroring the path-mappable rows of `docs/index.md`
§Documentation maintenance rules. `scripts/pr-triage.sh` feeds its classified
file rows to `scripts/doc-owners.sh`, which prints a `WARN` for every mapped glob
with at least 10 production lines changed (added plus deleted, `kind()` of the
triage) whose page is not in the change. A line `Docs: none - <reason>` in the PR
body silences the warnings and is echoed back, so the reviewer reads the reason.

- **A WARN, not a BLOCK.** `blocked` is untouched and `--gate` keeps its exit
  status. The rule becomes a BLOCK only when its measured false-positive rate on
  a sample of recent first-parent commits is low enough to trust; until then the
  contract reviewer reads the lines through its mandate and verifies each against
  the diff.
- **One page per row, one glob per row.** An area several pages describe maps to
  the page `docs/index.md` names first. A glob is a shell `case` pattern limited
  to `*` and `?`, where `*` also crosses `/`: plain bash 3.2, no new dependency.
  The tests run one table through that matcher and through their Go
  translation, so the two cannot disagree.
- **The logic is a script of its own**, a pure filter on stdin, so it is tested
  like `scripts/ci-filters.sh`; `pr-triage.sh` has no test of its own.
- **Fail open.** The call in `pr-triage.sh` prints one `WARN` line if the script
  fails and carries on: a broken map must not fail the required `plan-gate`
  check. `internal/repodocs` keeps the map valid (well formed, pages exist,
  every glob matches a file, every page is named in the maintenance table).
- **A row ships only if it is useful.** Over a sample of at least 100 commits, a
  row's page may be untouched in at most half of the commits that cross the
  threshold; the `internal/gitx` package as a whole failed that and is mapped to
  `realign.go` only.

**Why.** The docs drifted from the code because nothing noticed the page that was
not touched; the table in `docs/index.md` is read by whoever remembers it. The
cheapest signal that reaches author and reviewer is a line in the triage they
already run. The escape line keeps a refactor from being forced to edit a page it
does not change.

**Alternatives rejected.**

- Blocking now: the false-positive rate is the real risk, and a required check
  that cries wolf teaches authors to add the escape line by reflex.
- Inline in `pr-triage.sh`: it would ship without a test.
- A per-glob threshold or several pages per row: more configuration than the
  signal is worth.

**Consequences.** `plan-gate` runs the base branch's `pr-triage.sh` under
`pull_request_target`, so the PR that adds this feature does not exercise it; the
script reads the map and the PR body as data and executes nothing from the PR. A
glob that matches nothing disables its row silently, which
`TestDocOwnersGlobsMatchAFile` pins. A renamed file shows as `old => new` in
`git diff --numstat`, which no glob matches, so the pre-PR local run can
under-report a rename.
