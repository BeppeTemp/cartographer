---
name: kb-import
description: Agent-guided procedure to import an existing non-OKF wiki or knowledge base (Obsidian vault, markdown folder, wiki export) into a Cartographer KB, incrementally and without big-bang LLM rewriting.
version: "1.0"
---
# KB Import — Skill

## Purpose

Guide an agent (with its operator) through importing an **external corpus that does not follow
Cartographer/OKF patterns** — an Obsidian vault, a plain docs folder, an exported wiki — into a
Cartographer KB. The core principle (D74, consistent with D28: no server-side ingest tool):

- **Mechanical work is free**: layout mapping, frontmatter synthesis and link rewriting go through
  the `cartographer import` CLI scaffold — zero LLM tokens, cost proportional to corpus size.
- **Semantic work is agentic and incremental**: archive mapping, dedup and content curation are
  done by the agent, in small batches across sessions, driven by the `imported_draft` lint
  finding. Never rewrite a whole wiki in one session.
- **Human checkpoint before any write**: the mapping plan is approved by the operator first.

## Steps

### 1. Recon the source (read-only)
Inventory the corpus without modifying it: file count and formats, directory structure, link
style (`[[wiki]]` vs `[text](path.md)`), presence/shape of existing frontmatter, obvious
non-content (assets, templates, daily notes, trash). Produce a short summary for the operator.

> **Secrets check.** Grep the source for credentials/PII (`password`, `token`, `BEGIN.*KEY`,
> etc.) **before** anything is written to a KB that will be pushed. Anything found is excluded
> or moved to the SOPS flow — git history is forever.

### 2. Mapping plan — human checkpoint
Propose, and get the operator's explicit approval on:
- target **Maps** (existing ones, or new via `map_create`: `entities/`, `topics/`,
  `notes/`, `incidents/`, or custom) and the mapping *source directory → map[/expanded concept]*;
- what to **exclude** (assets, templates, generated files, empty stubs);
- whether the target is an existing KB or a new one (create it first with the `kb-create` skill).

**No write happens before this checkpoint.**

### 3. Mechanical scaffold (CLI, no tokens)
On a machine with the `cartographer` binary and a **local clone** of the KB repo:

```
cartographer import --source <src-dir> --kb <kb-clone> \
  [--default-map <map>] [--map <srcdir>=<map>]... --dry-run
```

Review the printed plan with the operator, then re-run without `--dry-run`. The scaffold:
synthesizes minimal frontmatter (title from the first H1 or the filename), preserves existing
frontmatter adding only missing fields, marks every concept `status: imported`, maps relative
source directories to a map, rewrites relative markdown links best-effort
(`[[wiki-links]]` stay as-is — first-class since D72). Writes go through the KB write path
(OKF invariants enforced), no per-file commits: make **one import commit** and push.

*Fallback*: if the binary predates `cartographer import` or the source is not markdown, do the
scaffold agent-side with `concept_write` in small batches (10–20 files), still marking each
concept `status: imported` — the rest of the procedure is unchanged.

### 4. Verify the import
- `atlas_overview` — structure matches the approved plan;
- `lint` on the imported scope — expect a backlog of `imported_draft` warnings (that is the
  curation queue, not an error) plus possible `broken_link`/`orphan`; fix only what is trivial;
- a couple of `search` spot-checks on known content.

### 5. Incremental curation (across sessions)
Per session, pick a **batch** (5–15) of `imported_draft` findings. For each concept:
1. read it (`concept_read`), improve frontmatter (summary, tags, `review_after` if factual);
2. fix links to real `[[id]]` targets; merge duplicates (`concept_move` batch does backlink
   rewrite; `supersede` for content replaced by a better page);
3. when the page meets KB standards, **remove `status: imported`** — that pops it off the queue.

Close each curation session with `log_append` (batch done, what remains). The marker makes the
backlog resumable by any future session — resist finishing it in one go.

### 6. Done
When `lint` reports no `imported_draft` in the imported scope, the import is complete: final
full `lint`, `log_append` with the closing summary.

## Reference

- Tools: `atlas_overview`, `map_create`, `concept_expand`, `concept_read`, `concept_write`,
  `concept_move`, `concept_list`, `supersede`, `lint`, `search`, `log_append`.
- CLI: `cartographer import` (see D74 WP2), `kb-create` skill for a brand-new target KB.
- Rationale and scope: `decisions.md` D74 (import), D28 (why no server-side ingest), D72
  (wiki-links, `concept_move` batch).
