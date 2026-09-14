---
topic: project-governance
---

# D101 — Tool names in the docs are CI-enforced

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
