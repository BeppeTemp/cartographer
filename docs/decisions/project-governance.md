# Project and documentation decisions

Repository governance, public release policy, planning and documentation
maintenance. Current contributor workflow:
[`CONTRIBUTING.md`](https://github.com/BeppeTemp/cartographer/blob/main/CONTRIBUTING.md).

These records explain why choices were made and may describe superseded behavior.
For the supported interface, follow the current-state page linked above.

<a id="d1"></a>
## D1 — Stdlib-only constraint ✅ removed
In the initial sandbox environment `go get` failed due to a MITM proxy: the M1–M3 code used stdlib only. The constraint has been removed. The hand-rolled implementations (YAML parser, MCP server) can be replaced with libraries when it makes sense.

---

<a id="d79"></a>
## D79 — Open source release: Apache-2.0, GitHub, release-please, GoReleaser, ghcr, Homebrew tap

**Status: implemented (2026-07-18).**

**Context.** The project lived on a private Gitea with an internal deploy-only CI (private registry + `homelab-manifests` bump). Publishing it required choices on license, hosting, history, pipeline, distribution and language.

**Decision.**

a) **License: Apache-2.0** (replacing MIT pre-publication, single author — a plain commit). No NOTICE, no per-file headers, **no CLA** (inbound = outbound) and no dual licensing: anyone, including the author's employer, may use, modify, close and monetize freely.

b) **Hosting: GitHub** (`github.com/BeppeTemp/cartographer`) as the **only** source of truth. The public history starts from a single squashed commit (`feat: initial public release`); the internal history remains readable on the archived (read-only) Gitea repo. The pre-existing version (`2.1.2`) lives only in the release-please manifest, not as tags.

c) **Pipeline: GitHub Actions** — `ci.yml` (required `test` check + `pr-title` conventional-commit lint), `release-please.yml` (release PR with semver bump computed from conventional commits; merge ⇒ tag + GitHub Release; needs the `RELEASE_PLEASE_TOKEN` fine-grained PAT so `pull_request` workflows trigger on the bot's PR), `release.yml` (GoReleaser: 4 binaries + `sha256sums.txt`, Homebrew **cask** pushed to `BeppeTemp/homebrew-tap` via `HOMEBREW_TAP_TOKEN`, multi-arch image on `ghcr.io/beppetemp/cartographer`). Both PATs are fine-grained and scoped to a single repo each.

d) **Main protection**: ruleset with mandatory PRs (0 approvals — CI is the review), required `test` check, no force-push/delete, **no admin bypass**; squash-merge only (the PR title becomes the commit on `main` that release-please reads). Daily consequence: no direct commits to `main` in this repo.

e) **Language: English everywhere** (docs, Go comments, e2e fixtures, project CLAUDE.md, skills); future plans and D entries are written in English. User-facing pages were reoriented to external users in the same pass (new `docs/getting-started.md`, two reading paths in `docs/index.md`).

f) **Homelab downstream**: the cluster consumes the public `ghcr.io` image (manifest bump in `homelab-manifests`, Flux reconciliation); the operator release procedure (`deploy` skill) moved out of the repo into the maintainer's local, unversioned tooling. First public release: `v2.2.0`.

**Rationale.** One pipeline instead of two (public release + private deploy were redundant); release-please removes per-release semver judgment; the squashed history avoids exposing internal timestamps while keeping the archive consultable; Homebrew via personal tap because homebrew-core is precluded to new projects (notability thresholds).

---

<a id="d80"></a>
## D80 — Versioning reset to 0.x: the public line starts at v0.1.0

**Status: implemented (2026-07-18).**

**Context.** D79.b carried the internal pre-publication version (`2.1.2`) into the release-please manifest, so the first public release came out as `v2.2.0`. Two problems. First, semantics: the project is a beta, and a 2.x number signals a maturity (two stable major cycles behind it) that the public project does not have — the 2.x history belongs to the archived internal line, not to what users can observe. Second, tooling: the module path is `github.com/BeppeTemp/cartographer` **without** the `/v2` suffix that Go modules require for v2+ tags, so `v2.2.0` was invisible to `go install`/`go get` (`@latest` resolved to a pseudo-version of `main`, never to the release).

**Decision.** The v2.x public line is retired and public versioning restarts at **v0.1.0**:

a) Manifest reset to `0.0.0` and `bump-minor-pre-major: true` + `initial-version: 0.1.0` in `release-please-config.json` (with no tag matching the manifest, release-please takes the initial-release path and would otherwise default to `1.0.0`): the next release PR computes `0.1.0`. Pre-1.0 semantics: **breaking changes bump the minor** (0.x convention), fixes bump the patch; `1.0.0` when the MCP tool surface and CLI stabilize.

b) The `v2.2.0` GitHub release and tag are deleted (operator action: they predate this decision and would otherwise remain "latest" and confuse the badge and `releases/latest`). The `ghcr.io` image tag `v2.2.0` and the Homebrew cask `2.2.0` are left as-is — registries are append-only in spirit and the next release overwrites `latest`/the cask. Homebrew will not auto-downgrade; irrelevant at current adoption.

c) `CHANGELOG.md` reset with a pointer to this entry; beta disclaimer added to the README (top, GitHub alert) and the pre-1.0 policy stated in `CONTRIBUTING.md`.

**Rationale.** A 0.x number is the only version string that tells the truth about stability, fixes the Go-tooling invisibility for free (0.x tags need no module-path suffix), and preserves headroom: the eventual real `v1.0.0`/`v2.0.0` stay meaningful. Resetting now, days after publication and before any registry (MCP Registry publish shipped after `v2.2.0` and never ran), is the cheapest moment it will ever be.

---

<a id="d81"></a>
## D81 — Agent-neutral workflow: AGENTS.md canonical, plans as GitHub issues

**Status: implemented (2026-07-20).**

**Context.** After the open source release (D79) the repo's agent-facing surface was still Claude-only: instructions lived in `CLAUDE.md` (a filename only Claude Code reads) and the design→implementation handoff in a Claude skill that committed plans to `docs/plans/` on `main`. That flow was broken by D79.d (protected `main`: landing a plan now takes one PR, deleting it another) and produced `docs(plans):` noise in the public history; a Claude-only repo was also inconsistent with Cartographer's own multi-provider configurator.

**Decision.**

a) **`AGENTS.md` is the canonical agent-instructions file** (the cross-agent convention read natively by Codex, OpenCode, Kiro and others); `CLAUDE.md` remains as a symlink to it, so Claude Code keeps working unchanged.

b) **Plans move from `docs/plans/` files to GitHub issues** (label `plan`, template `.github/ISSUE_TEMPLATE/plan.md`). The agent-neutral procedure — self-sufficiency test included — lives in `CONTRIBUTING.md` §Plan issues; `.claude/skills/plan` shrinks to Claude-side glue (D-number reservation, graphify pointers, `gh issue create`/`view`). The implementation PR closes the issue (`Closes #<n>`); a consumed plan survives as a closed issue instead of a deleted file.

**Rationale.** Issues give the handoff artifact first-class linkage to the PR, an archive that survives consumption, zero service commits in the public history, and readability from any agent with `gh` — the file-based flow had none of these once `main` became protected.

---

<a id="d82"></a>
## D82 — Beta marking via GitHub pre-release flag, not `-beta` version suffix

**Status: implemented (2026-07-20).**

**Context.** Until 1.0 the beta phase should be visible on the releases themselves, not only in the README disclaimer. Candidate mechanisms: a `-beta` semver suffix in the tags, or the GitHub pre-release flag on the releases.

**Decision.** `"prerelease": true` in `release-please-config.json`: every GitHub release is marked **Pre-release** until 1.0, tags stay plain `v0.x.y`. The already-published `v0.1.x` releases were retro-marked for consistency (operator action). The README release badge uses `?include_prereleases` and links to `/releases` (with every release a pre-release, `releases/latest` would freeze on the last stable one). At 1.0.0 the flag is removed and the badge reverted.

**Rationale.** A `-beta` suffix in the tags would re-break `go install @latest` (Go considers prereleases only when no release version exists, and stable `v0.1.x` tags already do — the exact invisibility D80 fixed) and is redundant with 0.x semantics, which already declare instability. The pre-release flag gives the visible marker at zero tooling cost: Go reads tags, not GitHub release metadata.

---

<a id="d98"></a>
## D98 — Planned work lives in GitHub issues, not in the docs

**Decision.** The docs describe the **current state** only. A feature that is
not implemented — deferred, "future work", "Phase 3" — is a GitHub issue
labelled `enhancement`; the page that touches it keeps the *current* limit
(what the code does today, so a caller can rely on it) plus a link to the
issue, and nothing about the plan. At the time, completed milestones and known
bugs still lived in `roadmap.md`; D110 later removed that remaining mutable
status page.

Applied by extracting six backlog items from the prose into issues #51–#56 (fine-grained RBAC/permission-aware retrieval/compliance audit; `mcp` per-artifact approval + server-side allow-list; Server git profile; real signature verification; `mcp` stdio transport and `env` emission; per-ref secret least privilege), and by removing from the table the rows already marked ✅ implemented — history, which belongs to the git log.

**Rationale.** Backlog inside the docs rots in a way prose can't signal: a reader cannot tell "this exists" from "we intend this", and the two drift apart silently. Two real cases found while doing this: `docs/concurrency.md` described the Server git profile's PR flow affirmatively (a table row and an `if_match` paragraph) with the only disclaimer buried in an earlier sentence, and D3/D13 still announced as "future" two things implemented since (SQLite index → D32/D43, wiki-links → D72). Issues also have what prose lacks: a state, an assignee, a closing PR.

**Consequences.** `docs/index.md` §Maintenance rules gained the routing row
("feature not implemented → issue, never prose"). A plan issue (label `plan`)
remains the design→implementation handoff for *scheduled* work:
`enhancement` is the not-yet-scheduled backlog.

---

<a id="d100"></a>
## D100 — Community surfaces: Pages from `docs/`, Discussions on, Wiki off

**Decision.** Three GitHub surfaces settled at once. **Wiki: disabled.** **Pages: enabled**, built by MkDocs Material from `docs/` (`mkdocs.yml`, workflow `.github/workflows/docs.yml`, `mkdocs build --strict`) — the markdown in `docs/` stays the single source, the site is only a rendering of it. **Discussions: enabled**, as the place for questions whose answer isn't documentation yet; when an answer stabilizes it is promoted into `docs/` through a PR.

**Rationale.** The wiki is a *separate git repo*: no PR, no `test` check, no review, and — decisive here — no way to change it in the same commit as the code, which is the project's central documentation invariant (D98, `docs/index.md` §Maintenance rules). It would have re-created, in a place nobody diffs, exactly the drift D98 removed. It adds nothing for agents either (they read `docs/` from the repo or by raw URL; `agent-install.md` is already the raw-URL-safe runbook, D97), and it would put runbooks that run `curl … | sh` and handle tokens somewhere editable without review. Pages gives the readability a wiki promises without giving up the PR flow.

**Consequences.** `--strict` makes a broken internal link a build failure, so moving a page breaks CI instead of the published site: the two `../` links out of `docs/` (`config.example.yaml`, `test/e2e/README.md`) became absolute GitHub URLs. The nav in `mkdocs.yml` mirrors the grouping of `docs/index.md`, which remains the canonical map — a new page goes in both.

---

<a id="d101"></a>
## D101 — Tool names in the docs are CI-enforced

**Decision.** `TestDocsToolNamesExist` (`internal/mcpserver/docs_test.go`) scans
`README.md`, `AGENTS.md`, `CONTRIBUTING.md` and current-state pages under
`docs/` for backticked snake_case identifiers carrying an MCP domain prefix
(`atlas_`, `concept_`, `map_`, `sync_`, …) and fails if one is not a
registered tool. It builds the **full** surface to compare against
(`AllowArtifactWrite`, a non-nil `BundleFS`), since artifact and bundle tools
register conditionally. The historical `docs/decisions/` directory is
excluded, where citing a since-renamed tool is correct. Homonyms that are not
tools (lint rules, frontmatter fields, the deliberately absent
`concept_collapse`) live in an explicit `docsNonTools` allow-list.

**Rationale.** Two renames (D66 `kb_overview`→`atlas_overview`, D77 `archive_*`/`dossier_*`→`map_*`) left dead names in the prose, and both survived until someone read the page by chance: the README was still advertising `kb_overview`, `archive_list` and `dossier_list` — the project's front page listing three tools removed months earlier, while a unit test asserted their removal from the *code*. Grep-based audits kept missing them because a tool is cited as `` `name` `` far more often than as `name(`. An allow-list is deliberately chosen over a heuristic: adding a legitimate homonym costs one line and a reason, whereas a clever regex silently stops catching things.

**Consequences.** A rename now fails CI until every page follows, which is the same discipline `mkdocs --strict` applies to links (D100). Same-session fixes: README §Key features and §Architecture (which still described the pre-D77 `archive → dossier → concept` hierarchy) and `sync.md`'s description of the generated instructions block — the code emitted `atlas_overview` correctly, only the doc was stale.

---

<a id="d110"></a>
## D110 — Topic-owned decision records and GitHub-owned project state

**Decision.** The monolithic `docs/decisions.md` becomes a short router to
topic-owned registers under `docs/decisions/`. Each D entry has one owner and a
stable `dNN` anchor; there is no duplicated global title/status table. The
obsolete `docs/roadmap.md` is removed. Issues own bugs, enhancements and plan
status; pull requests and releases own delivery; `CHANGELOG.md` summarizes
completed user-visible changes.

Plan issue titles reserve D numbers before implementation. Allocation therefore
checks both existing records and all plan issue titles. Implementation writes
the final entry to the one topic named by the plan. PRs in unrelated topics no
longer conflict merely because they both add decisions.

**Rationale.** The old register had grown beyond 1,700 lines while its duplicated
quick index stopped at D49, disagreed with later entries and hid stale product
claims. The roadmap repeated the same completed history while pointing mutable
work back to GitHub. Topic ownership keeps searches bounded and makes each
change update one current-state page, one rationale record and one external
status object at most.

**Consequences.** `docs/index.md`, MkDocs navigation, contributor guidance,
plan/implementation skills and the Plan issue template use the same routing.
Documentation tool-name validation walks nested current-state pages but skips
historical decision registers.

---

<a id="d111"></a>
## D111 — Repository E2E tests are deterministic and model-free

**Decision.** Remove the four OpenCode/LLM-driven scenarios and their
agent/sandbox helpers. Keep the six operator scenarios as the repository's
end-to-end suite because they deterministically exercise client configuration,
provisioning drift, git synchronization/conflicts and scoped authorization
through the compiled binary. Run that suite and the HTTP smoke test in CI.

Model interpretation and provider quality are evaluated in real usage, not as
a required repository check. `test/e2e/` now means protocol/process
end-to-end, not "an LLM agent performed the task".

**Rationale.** The LLM scenarios required an external endpoint, were never
executed by GitHub Actions and had no committed maintenance after the initial
public release. Their outcome depended on model availability and behavior.
The operator scenarios require no credentials and cover cross-component
boundaries that isolated Go tests do not.

**Consequences.** `make e2e` is deterministic and mandatory in CI;
`make e2e-quick` and the model-related environment variables are removed. The
HTTP smoke script moves from the otherwise-empty `scripts/` directory to
`test/smoke/`. The obsolete `make migrate` target is removed because its
referenced script no longer exists and wiki migration is provided by
`cartographer import`.
