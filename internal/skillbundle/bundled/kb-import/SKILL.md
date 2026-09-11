---
name: kb-import
description: Agent-guided procedure to import an existing non-OKF wiki or knowledge base (Obsidian vault, markdown folder, wiki export) into a Cartographer KB, incrementally and without big-bang LLM rewriting.
version: "1.1"
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
> etc.) **before** anything is written to a KB that will be pushed — git history is forever, so
> this check comes before any write and stays first. For each finding, decide explicitly between
> two outcomes and record the choice with the operator:
>
> 1. **Exclude** the file (or strip the value) from the import — the default for anything that is
>    not a credential the KB needs to resolve;
> 2. **Move the value into an encrypted file** and leave the concept referencing it via
>    `secret_refs`. The full procedure — age key, root `.sops.yaml` creation rules, the first
>    encrypted file, the `Service` concept — is the `kb-create` skill's
>    `references/secrets.md`. Cartographer performs none of the operator-side steps.
>
> If the corpus carries a `.sops.yaml` or `*.sops.yaml` of its own, do **not** merge it blindly
> into the target KB's rules: the recipients must be reconciled deliberately, because a wrong
> merge silently produces files nobody on the team can decrypt.

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
   Shortcut: `concept_patch(id, frontmatter: {status: null}, if_match, ...)` unsets the key in
   place, without a full `concept_write` rewrite (D88).

Close each curation session with `log_append` (batch done, what remains). The marker makes the
backlog resumable by any future session — resist finishing it in one go.

### 6. Done
When `lint` reports no `imported_draft` in the imported scope, the import is complete: final
full `lint`, `log_append` with the closing summary.

An imported corpus usually needs **artifacts** of its own — the skills, subagents and hooks that
configure the agents reading it, which no import scaffold can synthesize. Once the curation queue is
drained, `references/artifacts.md` in the `kb-create` skill is the procedure. For the operational
aftermath — connecting clients, syncing, diagnosing drift, upgrades — use the `cartographer-ops`
skill.

## Reference

- Tools: `atlas_overview`, `map_create`, `concept_expand`, `concept_read`, `concept_write`,
  `concept_patch`, `concept_move`, `concept_list`, `supersede`, `lint`, `search`, `log_append`.
- CLI: `cartographer import` (see D74 WP2), `kb-create` skill for a brand-new target KB,
  for authoring artifacts (`references/artifacts.md`) and for the SOPS flow
  (`references/secrets.md`); `cartographer-ops` for operations after the import.
- Rationale and scope: `docs/decisions/data-plane.md` D74 (import), D28 (why no server-side ingest), D72
  (wiki-links, `concept_move` batch).
