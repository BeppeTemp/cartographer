---
topic: project-governance
---

# D865 — The release changelog is grouped by area, in a post-processing step on the release PR

**Decision.** `internal/changelog.Regroup` rewrites the newest section of
`CHANGELOG.md` and of the release PR body as one `### <Area>` per area (Atlas, MCP
tools, Lint & repair, Client & CLI, Server & KB, General), each with its Features
and Bug Fixes, from the scope of every entry. The `release-pr` job of
`release-please.yml` runs it after release-please on every push to `main`
(`make changelog-areas`, plus `gen -stdin` on the body). `dev`, `demo`, `deploy`
and `ci` entries are dropped; synonym scopes (`webui`, `mcpserver`, `skills`,
`gitx`, …) map to their area; an unscoped entry or an unknown scope is General
(an unknown scope keeps its `**scope:**` prefix there, since no heading says it).
The `pr-title` check accepts only the canonical scopes and fails a PR whose body
has a wrapped conventional line.

**Why.** release-please groups by commit type only, so a release was one list of
20+ Features and one of 30+ Bug Fixes, sorted by scope, with entries cut
mid-sentence. The cut has one cause: release-please splits a squash message at
every line that parses as a conventional header and keeps only its first physical
line, and our bodies are hard-wrapped at about 72 columns. The grouping costs one
small Go package and a step in the job that already edits the release PR; the
truncation is stopped where it is written, in CI, because the rule had been
implicit and was broken in at least three releases.

The GitHub release is created from the merged PR body: release-please
(`strategies/base.ts` `buildRelease`) parses the body with `PullRequestBody.parse`,
takes the content between its `---` lines (single-release form: it must start with
the `## [x.y.z]` heading) and uses it verbatim as the release notes. So the body
is regrouped too, and its header, footer and version heading are untouched.

**Alternatives rejected.**
- One conventional-commit type per area (`atlas:`, `mcp:` …): changes the
  vocabulary every contributor and every tool reading types depends on.
- `changelog-sections` in `release-please-config.json`: it maps types to
  headings, not scopes to headings.
- A shell/awk step: the input is markdown with edge cases (multi-line entries,
  breaking changes, a Migration note) and `make gate` must cover it; hence Go with
  unit tests, as `internal/webui/gen` and `internal/testkbs/gen`.
- The `edited` trigger in `ci.yml`: fixing a body must re-run `pr-title`, but in
  `ci.yml` it would re-run the whole suite on every `gh pr edit` and on every
  body rewrite release-please does to its PR, cancelling the run in flight.
  `pr-title` is its own workflow (`pr-title.yml`); the ruleset requires the job
  name `pr-title`, not a workflow, so the required check is unchanged.

**Consequences.**
- Regroup never changes text it does not regroup (Migration note, version heading,
  PR boilerplate, breaking changes, any section other than Features and Bug
  Fixes), returns the input when it cannot parse an entry list, and is idempotent:
  the job runs after every push to `main` and feeds back what it wrote. A section
  with an area heading is returned as is.
- The scope list is written three times: the area table in `internal/changelog`,
  the `scopes` of `pr-title.yml` and `CONTRIBUTING.md`. A test fails when the
  workflow or the contributor guide drift from `CanonicalScopes()`.
- A scope in the table but not canonical (`webui`) groups correctly yet cannot be
  used in a new title; a scope in neither is General until it is added to the table
  and the list. `docs(readme)`, `test(git)` and similar scoped titles are no longer
  accepted: those types do not reach the changelog, so they go unscoped.
- A PR body edit by the job re-runs `pr-title` on the release PR; the job edits the
  body only when the grouping changes it.
