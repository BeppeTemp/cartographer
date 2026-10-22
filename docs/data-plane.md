# Data Plane — the Knowledge Base model

The data plane is the **source of truth**: UTF-8 `.md` files with YAML frontmatter, organized in a fixed hierarchy, versioned in git. The Go server holds no critical state: everything can be rebuilt from the files.

## Hierarchy

| Level | Name | What it is | OKF mapping |
|---|---|---|---|
| 1 | **Atlas** | A self-contained knowledge base; an instance hosts one or more | OKF bundle = git repository |
| 2 | **Map** / **Journal** | Map: a thematic domain with mixed `concept_types` (e.g. `smart-home`, `infra`). Journal: a chronological, append-oriented log (e.g. `incidents`, `notes`) | Top-level subdirectory, described by `_map.md` (`kind: map\|journal`) |
| 3 | **Concept** | A single knowledge page | `.md` file with frontmatter |

There is no intermediate categorization level (D77): category navigation is the job of curated `index.md` files, `search`, and the graph — not the filesystem. A growing concept becomes an **expanded concept** — a *state*, not a level: `concept_expand` turns `map/name.md` into `map/name/index.md` **without changing the ConceptID** (ID resolution tries `<id>.md` and then `<id>/index.md`, so no backlink breaks), and from there the concept can grow with `map/name/child` satellites and assets. Expansion is the prerequisite for owning assets. Expansion is also allowed in journals (e.g. a heavy incident with attachments). `concept_collapse` is its inverse (D160): `map/name/index.md` becomes `map/name.md` under the same ConceptID, so no inbound link changes. It refuses while the directory still holds satellites, assets or anything else besides `index.md` (a hidden file) — none would have a home after the collapse — and names them. `concept_merge` folds a satellite into its own parent, rebasing the merged body's relative links and redirecting every inbound link, including those from sibling satellites; both are `advanced`, i.e. callable by name but not advertised in `tools/list`.

Depth is **enforced on the write path** (D72 WP4): a ConceptID under `data/` has at most 3 segments (`map/concept/child`, where the third segment only exists inside an expanded concept); deeper writes are rejected. Reads are unaffected (legacy KBs remain readable). If a write implicitly creates a new expansion directory (e.g. `concept_move` into a nested path), the server also generates the `index.md` stub (`type: Index`, title from the name, listing the page that created the directory so neither is an orphan, D907) — so `index_get`'s progressive disclosure never breaks. Lint defends the semantics of the hierarchy (D77 WP4, `concept_oversize` D78): `expanded_missing_index` (a directory with no `index.md`), `expanded_ambiguous` (both `<id>.md` and `<id>/index.md` exist: writes are blocked until one form is removed), `expanded_as_category` (many children not linked from the concept's index: the directory is being used as a taxonomy), `map_oversize` (a map with more top-level concepts than its threshold, D313: a thematic split is preferable to a subfolder; satellites of an expanded concept and every journal entry are not counted, a journal never fires), `legacy_archive_descriptor` (a pre-D77 `_archive.md` descriptor), `concept_oversize` (a concept beyond the byte threshold: a candidate for `concept_expand` into a dossier).

Every KB (Atlas) is split into two planes: the **conceptual root** (`data/`), which holds maps, journals, and concepts; and the support folders (`skills/`, `services/`, `agents/`, `hooks/`, `templates/`), which sit directly under the KB root. KBs are **isolated**: no cross-links between different KBs.

## Filesystem layout of a KB

```
kb-<domain>/                          # git repo = OKF bundle (content directories only, D62)
├── .sops.yaml                         # creation_rules for encrypted secrets
├── .gitattributes                     # diff=sopsdiffer for *.sops.yaml
│
├── data/                              # CONCEPTUAL ROOT
│   ├── .gitignore                     # junk patterns, written by kb create (D316)
│   ├── index.md                       # root index — reserved
│   ├── log.md                         # global history — reserved
│   ├── smart-home/                    # MAP (kind: map, thematic domain)
│   │   ├── _map.md                    # descriptor (type: Map)
│   │   ├── index.md · log.md
│   │   ├── frigate.md                 # CONCEPT (plain form)
│   │   └── rete-thread/               # EXPANDED CONCEPT (same ID as before the expand)
│   │       ├── index.md               #   the main page
│   │       ├── topologia.md           #   satellite (smart-home/rete-thread/topologia)
│   │       └── evidence/flow.csv      #   ASSET (non-Markdown dossier file)
│   └── incidents/                     # JOURNAL (kind: journal, chronological log)
│       └── 2026-06-…-doppia-causa.md  # dated CONCEPT
│
├── services/                          # SERVICE DESCRIPTORS
│   └── keycloak.md                    # CONCEPT (type: Service)
│
├── skills/                            # domain SKILLS (agentskills.io)
│   └── <kb-ns>--<skill>/SKILL.md
│
├── agents/                            # SUBAGENTS (provisioning kind: agent, D48)
│   └── <name>.md                      # Claude subagent, single file
│
├── hooks/                             # HOOKS (provisioning kind: hook, D48)
│   └── <name>/
│       ├── hook.json                  # descriptor: event, matcher, command
│       └── <script>                   # executable invoked by the hook
│
├── templates/                         # KB-ONLY CONCEPT TEMPLATES (not provisioning artifacts)
│   └── <slug>.md                      # frontmatter + Markdown skeleton; rendered by concept_new
│
├── instructions.md                    # curated directives (D61), scaffolded by kb create (D325); frontmatter keys below
├── paths.yaml                         # optional: declared {{path:…}}/{{repo:…}} keys (D263)
├── settings.yaml                      # optional: the KB's own policy, set nowhere else (D746, D906)
└── glossary.yaml                      # optional: canonical terms, aliases, forbidden forms (D276)
```

`services/` is included in `WalkConcepts` (search, graph, lint all see it) but its root is `kb.Root`, not `kb.DataRoot()`. Service concept IDs carry the `services/` prefix. `ResolvePath` is the one place that picks the root for an ID, and every operation that turns an ID into a file — read, write, collision check, removal, `concept_move` — goes through it (`LocateConcept` for callers that move files themselves). For the same reason `services` is not a valid map or journal name: `map_create` refuses it, since the scaffold would land under `data/services/`, where no read looks (D269). A `data/services/` left by an older version is neither read nor cleaned up. `agents/` and `hooks/` are not concepts (no OKF frontmatter, they don't go through `WalkConcepts`): they are provisioning artifacts materialized client-side — see `docs/sync.md` §Agents and hooks.

`templates/` is outside `WalkConcepts`: templates have no ConceptID and are never indexed, linted or added to the graph. A template is a KB-only artifact, not a provisioning kind: it is maintained through `artifact_*`, discovered with `template_list`, and used once by `concept_new`; it never affects a provisioning manifest or its revision. A template is also a closed page schema that a map's pages bind to (§Templates are page schemas).

`kb create` writes `data/.gitignore` with the junk patterns (`.DS_Store`, `__pycache__/`, `*.pyc`,
`*.pyo`, `*~`, `*.swp`, `Thumbs.db` — `kb.JunkPatterns`), only when it creates the KB: the root
`.gitignore` stays absent (D62), and this one is content that travels with the KB (D316).

`kb create` also writes a short generic `instructions.md` at the KB root (status convention and write
discipline, English, no KB-specific names), only when it creates the KB and before the initial commit,
so a new KB never raises `missing_instructions`; an existing KB is left alone (D325).

`instructions.md` is folded into the client instructions with its frontmatter discarded (D61). Three
optional frontmatter keys are read by `lint` only (D316, D332):

```yaml
---
perimeter: ops                 # the KB's perimeter, named by each skill description
legacy_paths:                  # old path prefix -> replacement, for kb_repair legacy_path
  "wiki/operations/": "ops/"
  "wiki/": ""                  # empty: strip the prefix
---
```

`instructions.md` holds no suppression: a `lint_accept` key (D332) was removed by D907, because no
setting switches a check off. A file that still carries it gets `contract_legacy_key` on
`instructions.md` (fix `rewrite_contract`) and `kb_repair` drops the key; an artifact finding is
closed by fixing the artifact it names (§No check can be switched off).

`paths.yaml` is likewise KB-only data, not a concept and not a provisioning artifact: the KB's
declared placeholder vocabulary, maintained through `artifact_*` or git and served to clients by
`sync_pull` (§The path placeholder registry below). `glossary.yaml` is KB-only data too, read
only by `search` and `lint` on the server (§The glossary below).

### Assets

An **asset** is a regular, non-Markdown file inside an expanded concept directory: a CSV inventory, script, screenshot, document, or other dossier evidence. `asset_read`, `asset_list`, `asset_write`, and `asset_delete` use raw-byte SHA-256 `if_match` tokens; text and base64 preserve both UTF-8 and binary content. Asset paths are relative to the expanded owner, cannot be hidden, escape it, or end in `.md`, and are capped at 10 MiB — the largest file still worth versioning in git (D270): `asset_write` refuses a larger one and `asset_read` will not return it. Files also reach an expanded concept through git, so the listing never fails on one it can describe: hidden files and directories (`.DS_Store`, `.gitkeep`) are not assets and are skipped, and a file above the cap is listed with `oversized: true` and can still be deleted.

**Searchable text (D277).** `search` indexes the text of an owner's assets into the owner's own document, so a value that lives only in an inventory CSV, a config or a script finds the owner; the file is then found with `asset_list` and `asset_read`. An asset is indexed only if all hold: its extension is one of `.txt .csv .tsv .json .yaml .yml .toml .ini .conf .cfg .sh .ps1 .py .go .sql .xml .log`; it is not `oversized`; it is valid UTF-8 without NUL bytes, of which the first 256 KiB count (the rest is ignored). Binary formats (PDF, DOCX, images) are not extracted — that needs a dependency; write their content into the concept instead. The indexed text is the assets in path order, each as `<path>\n<text>`, so a path is searchable too. Changes are detected by stat signature (path, size, mtime), not by content, so an asset edited in place on disk is picked up by the next `search`.

An asset is **not** a concept: it has no frontmatter or ConceptID, is never emitted by `WalkConcepts`, validated as OKF, or made a graph node. Its text is searchable through its owner (D277, below); the asset itself is never a search hit. A dossier document can link to it with a relative Markdown file link. Lint reports a junk asset (`__pycache__/…`, `*.pyc`, see `data/.gitignore` above) as `junk_asset` (delete it with `asset_delete`) and never as `orphan_asset`, an uncited asset as `orphan_asset`, one above the cap as `oversized_asset`, and an owner whose assets cannot be listed (a symlink or special file inside it) as `unlistable_assets` rather than aborting the run. Moving an expanded concept moves its assets with it; inbound links from outside that directory to an asset are not rewritten. Deleting one requires explicit `force: true` when assets remain.

## Maps and Journals

Every map/journal declares its kind and the palette of allowed concepts in the `_map.md` descriptor:

```yaml
---
type: Map
title: Smart Home
kind: map                      # map (thematic) | journal (chronological log)
concept_types: [Entity, Topic, Runbook]   # a non-empty list makes the map strict (D907)
required_fields.*: [timestamp]            # optional, required on every concept by lint
required_fields.Runbook: [provenance]     # optional, additive for this exact type
field_values.*.status: [active, draft, deprecated, superseded]  # optional, allowed values on every concept (D275)
field_values.Incident.outcome: [open, mitigated, resolved]      # optional, per type; replaces the `*` list for that field
forbidden_fields: [state]                 # optional, fields no concept may carry
timestamp: 2026-06-25T10:00:00Z
---
```

The contract has one shape, keyed by type: `required_fields.<Type>` and `field_values.<Type>.<field>`, where `<Type>` is a concept type or `*` for every type. `map_list` returns `required_fields` and `field_values` for each map in that shape, so a client can check a value before writing (D343); the contract stays a lint check, not a write gate. `map_create` and `map_update` take `required_fields` (`{"*": [..], "Runbook": [..]}`) and `field_values` (`{"*": {"status": [..]}, "Incident": {"outcome": [..]}}`) as objects keyed the same way; an object replaces every `required_fields.*` / `field_values.*` key of the map and `{}` removes them. A type must be non-empty and have no `.`.

A **map** groups by theme, with mixed types (an Entity and a Topic from the same domain coexist: the type is a frontmatter attribute, not a position). A **journal** groups by chronology (dated concepts `YYYY-MM-DD-slug`, append-oriented). A map is **strict** (only `type`s in the palette) exactly when `concept_types` is non-empty, and flexible otherwise; the mode is derived and `map_list` reports it as `ontology_mode` (D907), so there is no `emergent` or `off` mode and nothing to set. `required_fields.*` is a map-wide lint contract; `required_fields.<Type>` adds fields for an exact, case-sensitive type. `field_values.*.<field>` restricts a frontmatter field to a list of exact strings (trimmed) on every concept; `field_values.<Type>.<field>` does the same for one type and **replaces** the `*` list for that field (the server does not merge them). A list-valued field is valid only if every element is allowed, an absent field is not checked (presence is `required_fields`' job) and an empty allowed list is a malformed key. `forbidden_fields` lists fields no concept may carry (D275). The server ships no default contract or domain vocabulary.

A `map_update` that **loosens** a rule (D907) — drops a required or forbidden field, a field's allowed values, the type list or the templates, or widens one — needs the write's `reason`, and `log.md` records it as `loosened: … (reason: …)`; a rule is changed because the pages should differ, not to make a finding go away. A change that only adds or tightens a rule needs none.

**Removed keys (D907).** A `_map.md` that still carries one of `lint_ignore`, `require_template`, `template_sections`, `require_index_entry`, `machine_path_allow_prefixes`, `repeated_fact_min`, `hotspot_in_degree`, `hotspot_bytes`, `oversize_bytes`, `oversize_concepts`, `title_max_length` or `ontology_mode`, the flat `required_fields: [..]` list, a flat `field_values.<field>`, or `stale_after: 0` / `harvest_after: 0`, is reported as `contract_legacy_key` on the `_map.md`; `map_list` shows them as `legacy_keys`. They take no part in the contract (a flat `required_fields` or `field_values.<field>` is still read as its `*` entry until it is moved). `kb_repair` (fix `rewrite_contract`, `kb.RealignContract`) drops the removed keys, moves the flat `required_fields` into `required_fields.*` and each flat `field_values.<field>` into `field_values.*.<field>`, and leaves every other key, the comments and the body as they are. `contract_legacy_key` is `AutoRepairSafe`, so the server's daily unattended repair realigns a KB in one commit after the upgrade; a second run finds nothing to do. Why each is gone: §No check can be switched off.

Read-compat (D77): the legacy `_archive.md` descriptor (`type: Archive`, `archive_type`) remains readable and is treated as a Map with `kind: map`; it is never written again, and lint flags it (`legacy_archive_descriptor`) as a migration backlog item; the repair does not rewrite it.

## Concept — anatomy of a page

A UTF-8 `.md` file with YAML frontmatter + a Markdown body.

```yaml
---
# --- OKF standard ---
type: Runbook                        # REQUIRED
title: Rotazione certificati TLS
description: Procedura trimestrale.
tags: [tls, sicurezza]
timestamp: 2026-06-25T10:00:00Z
# --- project extensions ---
status: active                       # draft | active | superseded | disputed | deprecated | archived
provenance: [https://internal.example.com/maintenance/cert-policy.pdf]
confidence: high                     # high | medium | low
valid_from: 2026-06-25
valid_to:                            # empty = valid now
superseded_by:                       # link to the claim that supersedes it
review_after: 2026-09-25
---
```

**Body**: conventional OKF sections (`# Schema`, `# Examples`, `# Citations`) plus `# History` / `# Updates` (append-only, counters *synthesis decay*).

**Typical page types**: `Entity`, `Concept`, `Summary`, `Runbook`, `IncidentReport`, `Postmortem`, `Asset`, `Checklist`, `Note`, `Reference`, `Service`, `Contradiction`.

## Reserved files

| File | Purpose |
|---|---|
| `index.md` | Content-oriented catalog (progressive disclosure). Reserved at the root and at the map level; inside an expanded concept it is the concept's own main page (same ConceptID as the directory). |
| `log.md` | Append-only chronological log, most recent entries first, with agent identity. |
| `_map.md` | Map/journal descriptor (type: Map, `kind`). |
| `_archive.md` | Pre-D77 legacy descriptor (type: Archive): read-compat only, never written again. |
| `AGENTS.md` | Legacy (D19, removed by D62): no longer generated by `kb.Init`, but remains reserved for KBs that still carry one from an earlier `Init`. |

## Cross-links and the graph

**Bundle-relative** links starting with `/` (stable, path from the KB root). A link A→B asserts a relationship (the prose supplies the type). Broken links are legitimate stubs. The emergent graph is what lint walks for scoping and is traversable both outbound and inbound (backlinks). It is derived from the concept files and kept in memory only, as a stat-validated cache (D241): every graph read enumerates the concept files and re-parses only those whose size, modification time or mode changed, whose modification time is too recent to vouch for them (within 2 s of when it was observed, git's "racily clean" rule), whose extensionless links' asset lookups now answer differently, or that are symlinks. An edit made by a tool, a git pull or an editor is therefore seen on the next read, with no restart and no reindex. What is cached is links, the content hash and a few frontmatter facets, never bodies; nothing is persisted, so a restart starts cold.

The graph retrieval tools (`graph_context`, `link_suggest`, `graph_path`, D242) run on the subgraph induced by the concepts the caller may see: hidden concepts are removed before anything is computed, so a narrowed token gets the answer the same tool gives on a KB in which those concepts do not exist, with no whole-KB restriction needed. `graph_context` and `link_suggest` treat a link as evidence of relatedness in both directions (the undirected projection); `graph_path` can also follow links only as written. Communities (Atlas colours, `atlas_overview` `structure`) weigh each link by the line that wrote it (D317): a normalised line carrying a link that N ≥ 5 visible concepts share is template boilerplate and its edges weigh 1/N; every other link weighs 1. The link lines are cached with the links from the same read, never the body. A missing concept and a hidden one produce the same `not found` error.

## Naming and concept IDs

Concept ID = path relative to the bundle without `.md`. File names are `kebab-case`. In journals, concepts are dated (`YYYY-MM-DD-slug`); in maps they have durable thematic names. A ConceptID never changes with expansion: `map/name` resolves to `map/name.md` or, once expanded, to `map/name/index.md`.

**Links between concepts** (both syntaxes are seen by the graph, lint, and `concept_move`'s backlink-rewrite, D72 WP0): wiki-links `[[id]]` / `[[id#section]]` with **root-relative** IDs (path from the KB root without `.md`, e.g. `[[smart-home/otbr]]`); markdown links `[text](rel/path.md)` **relative to the file** containing them; the label may hold one level of square brackets (`[Issue [RFC] x](x.md)`), and the graph, `index_incomplete` and lint all parse it with the same pattern (D720). The alias form `[[id|text]]` **is** supported (D150) and is what keeps a readable label on the one form that is base-independent; the label is preserved verbatim by `concept_move`. **Fenced code blocks and inline code spans are not scanned** (D150): a Mermaid subroutine node (`N1[["a label"]]`), a POSIX character class (`grep -E "x[[:alpha:]]"`) or a documented example link inside a fence is not a link. `concept_move` does not rewrite one either (D248): it rewrites exactly what the graph extracts. Indented four-space blocks are deliberately still scanned, being indistinguishable from a list continuation. An **extensionless** href that resolves to an existing asset of the citing concept (a `Dockerfile`, a `Makefile`, a `LICENSE`) is treated as an asset citation rather than a ConceptID shorthand, so such an asset can be cited and stops being reported `orphan_asset`.
**One base, and it is the file** (D149). A relative markdown link resolves against the file that contains it, exactly as a markdown viewer resolves it — so from an expanded concept's `index.md` a satellite is `[s](s.md)`, not `[s](c/s.md)`. The graph and lint use that same base; lint's existence check goes through the same resolver `concept_read` uses, so a link to the canonical ID of an expanded concept (`[c](c.md)` where `map/c/index.md` holds it) is valid rather than broken. A link spelled `c/index.md` or `[[map/c/index]]` is the same concept, not a node of its own (D310): the link graph (`buildGraphView`) stores the edge under `map/c`, so backlinks, orphan, `cut_concept`, communities and `link_suggest` count it; `ExtractLinks` and `RewriteLinks` still report the literal ID. Lint's `index_link_form` (`kb_repair`-fixable) flags the spelling. A wiki-link `[[id]]` is root-relative and therefore base-independent, which is why it is the safe choice when in doubt.

**In a curated index, both forms are accepted** for an expanded concept: `[c](c.md)` and `[c](c/index.md)` both count as the entry `index_incomplete` looks for. The two used to be inverses of each other — one valid between concepts, the other in an index — with nothing to tell them apart.

### Frontmatter value forms

A value may be a scalar, a block list (`- item` on following lines), or a flow list `[a, b]` — the
flow form **on one line or across several** (D162), which is what any editor produces when a list gets
long:

```yaml
provenance: [
  first/source.md,
  second/source.md,
]
```

A trailing comma before `]` is accepted and yields no empty element. A `]` inside a quoted element is
not a terminator. An unclosed flow list is still an error and names the key **and the line**;
`validate` wraps that with the concept's path, so a malformed value is locatable in a corpus of a
thousand pages. The serializer always emits the single-line form: a multi-line source round-trips into
one line, which is reflow, not data loss.

Single-quoted YAML values decode a doubled apostrophe as one apostrophe (`'Don''t panic'` becomes
`Don't panic`), including in block and flow lists. Apostrophes in double-quoted values stay as written.

A key whose value is empty and whose following lines are **indented** holds a nested block (D291), for
example an agent's `providers:` map. The parser keeps those lines verbatim as an `okf.Block` value under
that key instead of flattening them into sibling keys (which made a nested `tools:` look like the
agent's own); it does not interpret them, and the serializer writes them back unchanged.

### Serialization guarantees

A write never leaves frontmatter that its own parser cannot read back (D309):

- A scalar is quoted whenever it holds a byte the flow-list parser treats as a delimiter or a quote —
  `'` included, so `dall'operatore` is written `"dall'operatore"`. A `\"` inside a quoted flow element
  does not close it.
- Overwriting or removing a key also removes the indented lines its old value left behind
  (`provenance: text` followed by `  - item` lines), never a `#` comment at column 0 or a blank line
  that separates keys.
- Blank lines before the first key are dropped: a file never starts with `---` and an empty line. A
  comment there is kept.
- As a last guard, the write path parses the serialized frontmatter before the file is written and
  rejects the write (`frontmatter round-trip check failed: …`) if it does not parse.

### What a move touches

`concept_move` is complete as of D160: it rewrites **inbound** links across the KB — reading only the
concepts the link graph says link to a moved id, plus those whose `superseded_by` names one (D248) — **the moved
pages' own relative links** — and **both curated indexes** (D907: every curated index must list its map's concepts, so every one is maintained; a `generated` index is the server's, §Generated indexes), removing the source entry and
appending one to the destination under a `## Moved here` heading as the plain entry `- [title](path)`, with no suffix (D720).

The own links are arithmetic, since the directory delta is known. An expanded concept's ID has two segments, so a move
changes the directory it sits in, never its depth, and a satellite's `../x.md` names a sibling of the old parent just
as `index.md`'s does: `index.md` and every satellite (nested ones included) are rebased from their old path to their
new one. Links between the pages of the concept, and to its assets, keep resolving to them.

Both index edits are conservative. A source entry is removed only when the moved concept is its **only**
link: a line citing two concepts is prose the operator wrote, so it is kept and reported instead. The
entry is the whole list item: its first line and the lines that continue it (a wrapped description, a nested
sub-list), up to a blank line or a line indented no deeper than the item. A nested item that cites another
concept keeps the entry, reported like a multi-concept line (D826).
Cartographer does not attempt to place the destination entry in the right thematic section — it cannot
know, and a wrong placement in a curated document is worse than an obvious one at the end.
`rewrite_links: false` still means "touch no other concept", so it skips the index maintenance too.

`concept_archive` (D344) is the move of a retirement and goes through the same move core, so it maintains both curated indexes the same way; a line citing several concepts is kept and reported.

None of this depends on the namespace: a move from a map into the KB-root `services/`, the reverse,
or a rename within `services/` has the same postconditions as one between two maps — the old ID is not
found, the new one is readable, an expanded concept carries its satellites and asset bytes, and search
follows (D269). Every entry of a batch is validated — IDs, path confinement, source present, destination
free in its real root, duplicates, overlapping expanded moves — before any entry is applied. The
boundary is a filesystem failure after a destination was written (the source cannot be removed, or an
expanded directory cannot be renamed): the call returns an error naming both IDs and what was already
applied, nothing is rolled back, logged or committed, and the operator deletes the extra copy.

### Generated indexes (D301)

A map whose contract says `index: generated` (set with `map_update`; `curated` or `""` removes it)
hands its concept list to the server. After every successful write, before the commit, the server
recomputes for each such map the block below from the graph cache and rewrites `index.md` only if
the bytes differ — in the same commit as the write, so creating a concept in a 100-entry journal is
one added line, not a re-sent index. Every map is checked, not only those the write touched: the
work is in memory, and an index edited out of band heals on the next write.

```
<!-- cartographer:index begin — generated from this map's concepts; edit outside the block -->
### <Type>
- [[<id>]] — <title>
<!-- cartographer:index end -->
```

The entries are the map's **direct** concepts (two-segment IDs: an expanded concept is one entry,
its satellites none), titled from the frontmatter or by the ID. A map lists them by title then ID,
under a `### <Type>` heading only when it holds at least two types (untyped last, as `Other`); a
journal newest ID first, grouped by `### YYYY-MM` past 30 entries (IDs with no date prefix last,
under `Undated`). The same concept set always renders the same bytes. The block is appended after
the curated text the first time and replaced in place afterwards; every byte outside it is the
operator's. `index_patch` refuses an edit that changes the block (`generated_index`), `concept_move`
leaves a generated index to the server instead of editing it, and `lint` reports `index_stale`
(directory-level) when the block differs from what the next write would put there — in place
of `index_incomplete`, which a generated map never gets. A KB's `instructions.md` that tells agents
to add new pages to the index is the `kb-doctor` skill's to update, not the server's.

### Cost thresholds (D301)

`index: generated` is the one cost key a map sets: the server keeps the map's concept list (§Generated indexes); `curated` or `""` removes it. Every other threshold of the cost kinds is a constant (D907): no map overrides it, because a threshold a map could raise is a check a map could switch off.

| Check | Fires when | Constant |
|---|---|---|
| `repeated_fact` (review item, and the write-time finding of the written concept's map, D351) | a normalised line of at least 40 bytes is carried by at least 3 concepts | `repeatedFactMinBytes = 40`, `repeatedFactMinConcepts = 3` (`internal/lint/review_cost.go`) |
| `read_hotspot` | a concept has at least 50 inbound links and a body of at least 16 KiB | `hotspotMinInDegree = 50`, `hotspotMinBytes = 16 KiB` |
| `concept_oversize` (observation, D947) | a concept's body is over half the 60 KB read guard (30000 bytes) | `conceptOversizeThreshold` (`internal/lint/lint.go`) |
| `map_oversize` (observation, D313, D947) | a map has more than 50 top-level concepts | `mapOversizeThreshold = 50` |
| `title_quality`, length rule (D315) | a title is longer than 100 characters | `defaultTitleMaxLength = 100` (`internal/lint/conformance.go`) |

The reply is the page: keep the fact in one concept and link to it, split a hotspot or an oversized concept into satellites (`concept_expand`), split an oversized map, shorten the title. `concept_oversize` and `map_oversize` are observations, not lint findings (§Observations, D947): `kb_review` lists them, no write is refused or told of them.

Page-name key (D315), read by `title_quality`:

| Key | Meaning | Default |
|---|---|---|
| `forbidden_title_terms: [...]` | substrings no title in this map may carry, matched case-insensitively | none |

`forbidden_title_terms` is a non-empty list; anything else is `contract_malformed`. `map_update` sets it.

### No check can be switched off (D907)

Nothing in a KB switches a lint check off or hides a finding ([D907](decisions/D907-the-lint-cannot-be-escaped.md)). Every finding is closed by fixing the page or the map contract; a Health panel with no findings is what a finished `kb-doctor` session leaves, and it means the pages are right, not that something was set aside. A false positive is a defect of the check: report it and the check is fixed for every KB, instead of one page silencing it. What was removed:

- `lint_ignore` in a concept's frontmatter (D159) and in a map's `_map.md` (D306), and `lint_accept` in `instructions.md` (D332);
- the per-map thresholds and opt-outs: §Cost thresholds, §Decay checks, §Templates are page schemas, §Maps and Journals (**Removed keys**);
- the suppression inventory (D834: `lint.RunWithSuppressions`, `kb_status.suppressions`), `lint.CheckAcceptability` and `kb_status.conformance.acceptability` (D313), and the check `lint_ignore_invalid`;
- the dismissal of a `kb_review` item (§Review keys): each kind closes when the pages it names are in the state it asks for.

The legacy keys are reported and dropped, never honoured:

| Where it is found | Finding | Fix |
|---|---|---|
| `lint_ignore` or any other removed key in a `_map.md` | `contract_legacy_key` on the `_map.md` | `rewrite_contract` (`kb.RealignContract`) |
| `lint_accept` in `instructions.md` | `contract_legacy_key` on `instructions.md` | `rewrite_contract`: the repair drops the key |
| `lint_ignore` in a concept's frontmatter | `nonstandard_field` (§Conformance checks) | `drop_field` |

`kb_repair` applies each, and so do the unattended repairs (the checks are auto-repair safe).

`index_lists_retired` (D344) is one finding per retired concept that a curated index of a live map still lists (D366; one per index before); journals, generated indexes and maps where at least half of the top-level concepts are retired (an archive) are exempt. That rule is `kb.IsArchiveMap`, the one function the lint exemption and the overview's `archive` flag both ask, which the Atlas's rail shows as an "Archive" badge (D820); no contract field marks an archive. `concept_archive` removes the entry; the finding catches a hand-made retirement. It carries `drop_index_entry` (D366, `field` = the concept; auto-repair safe, in `lint.AutoRepairOrder`) when a line of the index lists that concept and nothing else, and the repair drops that line (the code `concept_archive` uses); a concept cited only on lines with other links keeps no fix and stays with the doctor. The finding's path is the index, so the repair is a map repair (`lint.MapRepairCheck`), not a concept write. A retired concept left in a live map is never asked back in: `index_incomplete` skips it (D826), so the entry the repair drops does not come back as a finding on the next run.

Structural checks flag defects, not structure (D313): `link_to_retired` is **one finding per retired
concept**, on the retired concept ("retired, still linked by N live concepts: …"; the declared
`superseded_by` successor and linkers in a journal are not counted), so retiring a component is one
decision; when the retired concept declares a `superseded_by` that resolves to a **live** concept (one hop: a retired successor, an unresolvable one or none keeps the finding as judgement) the finding carries `retarget_links` (D366: `field` = the retired concept, `to` = the successor, `targets` = the linkers it counts; cross-concept, auto-repair safe), and the repair points every markdown link and wiki-link of those linkers at the successor, keeping label, anchor and alias; a linker that already linked the successor ends up with a repeat the existing `repeated_link` / `duplicate_link` fixes remove on the same run;
The `cut_concept` observation does not count an expanded concept's own satellites or a journal's entries among the
nodes a vertex separates; `secrets_on_non_service` points at `secret_resolve`; and
`machine_path` never fires for the conventional tool paths `~/.ssh/`, `~/.kube/`, `~/.m2/`,
`~/.config/`, `~/.cache/`, `~/.local/`, `~/.gnupg/` (a fixed list; no map adds to it, D907). The `missing_value_contract` observation skips a field whose every value is a date, and a field whose values are identifiers: every value URL-shaped, or each of at least three carrying concepts holding its own value (D720).

`orphan` is a concept no other concept links to. A page cited in another page's frontmatter `provenance` is not an orphan: being cited is a way in (D720). A retired concept (`deprecated`, `superseded`, `archived`) is never an orphan: `link_to_retired` and `index_lists_retired` push links away from it, so asking for one would contradict them (D826). The same rule runs the other way in `link_to_retired`: a linker that is itself retired does not count as a live one, so a retired parent of an expanded concept is not a live linker of its satellites.

### Active checks (D371)

Every check runs by default, and no map or page can turn one off (D907). A check is **inactive** on a KB only when it has nothing to compare against: the template checks when no map declares `templates` or `default_template` (`template_section_missing` included), `unknown_type` without a type palette, `stale_open` when no map ages its open pages (a journal, or a map with `open_statuses` or `stale_after`), `missing_required_field` / `invalid_field_value` / `forbidden_field` when no map declares the matching key, `index_incomplete` when every map has `index: generated` (which replaces it by construction), `index_stale` without a generated index, `forbidden_term` without a forbidden term in `glossary.yaml`, `unknown_placeholder` / `unused_placeholder` without `paths.yaml`, `sops_missing_file` without `secrets/`, `legacy_path` without `legacy_paths` in `instructions.md`, and `source_uncited` without a `Source` concept. `lint.Inactive` computes this and `GET /api/ui/v1/kbs/<kb>/checks` reports it (`active: false` and a `reason`); an inactive check's zero in the Atlas Health coverage is greyed out, not counted clean. There is no opt-out to name: the check becomes active the moment the KB declares what it compares against. A check that would fire once per page on a map that never chose it is not made default: a map with no templates is not a finding at all, and `kb_review`'s `template_proposal` proposes the ones its pages already follow (D947).

### Check catalogue (D354)

Every lint check is one entry in `internal/lint/registry.go`; the tables that say which checks are
fixable, which a write could have avoided, and which are listed on the Artifacts panel are
projections of it. This catalogue is generated from it (a test fails when it drifts):

- **Level**: `concept` (one page, its map contract; evaluated by one function for `lint` and for the
  write path), `graph` (needs the link graph or KB-wide data), `map` (a map or an expanded concept's
  directory), `artifact` (a KB-root file, only an unscoped lint), `kb` (a KB-level file or a
  `kb_review` kind).
- **Avoidable at write** (D907): a write that introduces a finding of this check could have avoided
  it within the same call. `-` marks a check no write can avoid; the reason is in §Which checks a
  write could have avoided below.
- **Fix**: the fix kinds `kb_repair` applies. **Auto-repair safe**: the fix needs no person, so the
  unattended repairs run it (`lint.AutoRepairChecks`: write, heartbeat and `kb repair --apply`; no setting narrows them, D907). **On write**: a write response evaluates it
  (`CheckConcept` for `concept`, `ScopedCheck` for `graph` and `map`).
- A finding has no severity (D947): every finding of a registered check is a problem, built through one constructor that reads the registry. Findings are listed in the registry's category order (`lint.Categories`), then by check, then by path: an order to start from, not a judgement of gravity.
- **Auto-repair runs to a fixpoint (D355).** Repairs depend on each other (a field rename exposes a
  prose value, a split exposes an invalid value), so the unattended paths never apply one check per
  pass. Stage 1 repairs each concept to a fixpoint: re-evaluate the in-memory content with
  `CheckConcept`, apply the allowed checks' fixes, repeat until none is left (at most 8 passes; a content
  seen twice is an oscillation), then write the concept once. A concept that does not converge is not
  written and is reported as `did not converge: <checks>` (stderr and the run log). Stage 2 runs the
  `CrossConcept` checks one by one through the per-check applier (it keeps the mutual-pair guard); if it
  changed anything, stage 1 runs once more on the concepts it touched. Only `Auto-repair safe` checks run
  unattended. The operator's `kb_repair` tool is unchanged (any
  fixable check, one at a time, dry run by default).

<!-- lint:catalogue:begin — generated from internal/lint/registry.go; run `go test ./internal/repodocs -run TestLintCatalogueIsUpToDate -args -update` -->
| Check | Level | Avoidable at write | Fix | Auto-repair safe | On write |
|---|---|---|---|---|---|
| `malformed_frontmatter` | concept | yes | - | - | yes |
| `stringified_list` | concept | yes | `listify_field` | yes | yes |
| `stale_claim` | concept | - | - | - | yes |
| `status_semantics` | concept | yes | - | - | yes |
| `machine_path` | concept | yes | - | - | yes |
| `mangled_placeholder` | concept | yes | - | - | yes |
| `missing_title` | concept | yes | `set_value` | yes | yes |
| `title_h1_mismatch` | concept | yes | `sync_h1`, `set_value` | yes | yes |
| `title_quality` | concept | yes | `set_value` | yes | yes |
| `missing_required_field` | concept | yes | - | - | yes |
| `invalid_field_value` | concept | yes | `set_value` | yes | yes |
| `forbidden_field` | concept | yes | - | - | yes |
| `nonstandard_field` | concept | yes | `rename_field`, `drop_field` | yes | yes |
| `tool_param_field` | concept | yes | `drop_field` | yes | yes |
| `prose_value` | concept | yes | `split_value` | yes | yes |
| `stale_open` | concept | - | - | - | yes |
| `closed_with_open_items` | concept | yes | - | - | yes |
| `template_section_missing` | concept | yes | - | - | yes |
| `template_missing` | concept | yes | `set_value` | yes | yes |
| `template_unknown` | concept | yes | - | - | yes |
| `template_not_allowed` | concept | yes | - | - | yes |
| `template_type_mismatch` | concept | yes | `set_value` | yes | yes |
| `template_field_missing` | concept | yes | - | - | yes |
| `template_field_value` | concept | yes | `set_value` | yes | yes |
| `template_extra_section` | concept | yes | - | - | yes |
| `template_section_alias` | concept | yes | `rename_heading` | yes | yes |
| `template_section_order` | concept | yes | `reorder_sections` | yes | yes |
| `open_marker` | concept | yes | - | - | yes |
| `repeated_link` | concept | yes | `unlink_repeat` | yes | yes |
| `missing_frontmatter` | concept | - | `add_frontmatter` | yes | - |
| `unparseable_frontmatter` | concept | - | `quote_value` | yes | - |
| `missing_type` | concept | - | `set_value` | yes | - |
| `concept_too_deep` | concept | - | - | - | - |
| `nonslug_file_name` | concept | yes | `move` | yes | - |
| `empty_concept` | concept | yes | - | - | - |
| `imported_draft` | concept | - | - | - | - |
| `secrets_on_non_service` | concept | yes | - | - | yes |
| `sops_format_mismatch` | concept | yes | - | - | yes |
| `sops_missing_file` | concept | yes | - | - | yes |
| `legacy_path` | concept | yes | `replace_prefix` | yes | yes |
| `broken_link` | graph | yes | `rebase_link` | - | yes |
| `duplicate_link` | graph | yes | `drop_link_item`, `rewrite_link_item` | yes | yes |
| `reciprocal_link_item` | graph | yes | `drop_link_item` | - | yes |
| `bare_link_list` | graph | yes | - | - | yes |
| `index_link_form` | graph | yes | `rebase_link`, `rewrite_wiki_link` | yes | yes |
| `orphan` | graph | yes | - | - | yes |
| `broken_relation` | graph | yes | - | - | yes |
| `link_to_retired` | graph | yes | `retarget_links` | yes | yes |
| `unknown_placeholder` | graph | yes | - | - | yes |
| `forbidden_term` | graph | yes | - | - | yes |
| `source_uncited` | graph | - | - | - | - |
| `unknown_type` | graph | yes | `set_value` | - | - |
| `value_case_variant` | graph | yes | `set_value` | yes | - |
| `contract_malformed` | map | yes | - | - | - |
| `legacy_archive_descriptor` | map | - | - | - | - |
| `contract_legacy_key` | map | - | `rewrite_contract` | yes | - |
| `index_incomplete` | map | yes | - | - | yes |
| `index_lists_retired` | map | yes | `drop_index_entry` | yes | - |
| `index_stale` | map | - | - | - | - |
| `expanded_missing_index` | map | yes | - | - | - |
| `expanded_ambiguous` | map | yes | - | - | - |
| `expanded_as_category` | map | yes | - | - | - |
| `unlistable_assets` | map | yes | - | - | - |
| `oversized_asset` | map | yes | - | - | - |
| `orphan_asset` | map | - | - | - | - |
| `unmapped_folder` | map | yes | `scaffold_map` | yes | - |
| `stray_file` | map | yes | - | - | - |
| `skill_invalid` | artifact | yes | - | - | - |
| `skill_warning` | artifact | yes | - | - | - |
| `legacy_tool_name` | artifact | yes | `strip_tool_prefix` | yes | - |
| `skill_broken_ref` | artifact | yes | - | - | - |
| `skill_git_command` | artifact | yes | - | - | - |
| `skill_missing_perimeter` | artifact | yes | - | - | - |
| `cross_kb_path` | artifact | yes | - | - | - |
| `junk_file` | artifact | yes | - | - | - |
| `symlinked_path` | artifact | - | - | - | - |
| `junk_asset` | artifact | yes | - | - | - |
| `missing_instructions` | artifact | - | - | - | - |
| `artifact_unused` | artifact | - | - | - | - |
| `hook_invalid` | kb | yes | - | - | - |
| `missing_registry` | kb | - | - | - | - |
| `unused_placeholder` | kb | - | - | - | - |
| `duplicate_candidate` | kb | - | - | - | - |
| `zombie_work` | kb | - | - | - | - |
| `promotion_candidate` | kb | - | - | - | - |
| `glossary_gap` | kb | - | - | - | - |
| `lint_judgement` | kb | - | - | - | - |
| `repeated_fact` | kb | - | - | - | - |
| `read_hotspot` | kb | - | - | - | - |
| `scattered_work` | kb | - | - | - | - |
| `status_reclassify` | kb | - | - | - | - |
| `harvest_candidate` | kb | - | - | - | - |
| `template_proposal` | kb | - | - | - | - |
| `map_naming` | kb | - | - | - | - |
| `observation` | kb | - | - | - | - |
<!-- lint:catalogue:end -->

#### Which checks a write could have avoided (D907)

Every other check is avoidable at write, and the write gate refuses a write that introduces one (D907). The registry field is `AvoidableAtWrite`; these are the checks it is false for, grouped by why no write can avoid them:

| Why | Checks | Reason |
|---|---|---|
| Time | `stale_claim` | a `review_after` date that has passed: the page was fine when written |
| Time | `stale_open` | an open page older than the map's effective `stale_after`: it ages without a write |
| Legacy state | `legacy_archive_descriptor` | a `_archive.md` descriptor from before D77; no write creates one |
| Legacy state | `contract_legacy_key` | a `_map.md` or `instructions.md` that still carries a key D907 removed; `kb_repair` drops it |
| The KB's initial state | `missing_instructions` | the KB has no `instructions.md` yet, and the finding has no file to name |
| The KB's initial state | `missing_registry` | placeholders are cited and the KB never got a `paths.yaml` |
| Work another tool does | `source_uncited` | a `Source` marked `ingested` that no page cites: the ledger and the pages built on the source are different writes (`source_register`, `concept_write`) |
| Work another tool does | `imported_draft` | `status: imported`, set by `cartographer import` until the page is curated |
| Work another tool does | `index_stale` | the generated index block differs from the graph: the server rewrites it after the write, in the same commit (§Generated indexes) |
| Work another tool does | `orphan_asset` | an asset no page of its dossier cites: `asset_write` and the page that cites it are different calls |
| Not created by a write | `symlinked_path` | a symlink arrives with a git checkout or a hand edit; no tool creates one, and concept and asset paths refuse it |
| Appears with time, not with a write | `artifact_unused` | a skill or agent is flagged by the usage reports and the clock (D1043); adding one must never be refused for not having been used yet |
| Refused by the write path before lint | `missing_frontmatter`, `unparseable_frontmatter`, `missing_type`, `concept_too_deep` | the write is refused before it is linted, so the refusal is already the gate; `lint` reports a page that reached disk another way |
| Whole graph, map or KB, not evaluated by the write path | `unused_placeholder` | a `paths.yaml` key no page or artifact cites: it depends on every page |
| Not a lint finding | the thirteen review kinds: `duplicate_candidate`, `zombie_work`, `promotion_candidate`, `glossary_gap`, `lint_judgement`, `repeated_fact`, `read_hotspot`, `scattered_work`, `status_reclassify`, `harvest_candidate`, `template_proposal`, `map_naming`, `observation` | a `kb_review` item of the doctor's list, not gated: each closes when the pages it names are in the state it asks for (§Review keys) |

### Observations (D947)

A lint finding is a problem: there is no severity, `gate_check` passes only with none, and a write that introduces an avoidable one is refused. A check therefore stays in the lint only if it has a **definite fix**: a state of the KB that makes the finding disappear and is always better than the current one. A state a reasonable maintainer could look at and decide "this is fine as it is" is an **observation**. It is not a finding: `lint`, `gate_check`, a write's response, the safe repairs and the Atlas Health problems never carry one. `kb_review` lists it, as an item of kind `observation` whose `check` names it, so the doctor session, where a person decides, is the only place it appears. Like every review item it closes on page state, never on a dismissal (D907): the generated table says which state, and a test writes it for each observation.

An observation that no page state could close, or that another tool already reports, is not an observation but deleted: `map_without_templates` (the `template_proposal` kind proposes the templates a map's pages already follow), and the `unknown_type` finding in a map that has not adopted types (`template_proposal` again).

<!-- lint:observations:begin — generated from internal/lint/observations.go; run `go test ./internal/repodocs -run TestLintObservationsAreUpToDate -args -update` -->
| Observation | Category | Whole KB only | Why it is not a problem | The item closes when |
|---|---|---|---|---|
| `cut_concept` | links | yes | a hub that holds pages together is often the right shape of a wiki | removing or unlinking the concept disconnects fewer than 3 concepts |
| `island` | links | yes | two pages that belong only to each other are a legitimate pair | a page outside the group links into it, or one of its pages links out |
| `map_misfit` | links | yes | where a page belongs is a matter of taste once its links point elsewhere | the concept moves to the map its links point to, or its links no longer mostly point to another map |
| `concept_oversize` | pages | - | a reference table or a long procedure may be one thing at any length | the body is at most 30000 bytes |
| `map_oversize` | maps | yes | a map grows by construction and none of its pages is the one that is wrong | the map has at most 50 top-level concepts |
| `facet_sprawl` | maps | yes | tags are a free vocabulary, and which values to merge is an editorial call | fewer than 30 distinct tag values, or fewer than half of them used once |
| `missing_value_contract` | maps | yes | whether a field is a closed vocabulary is the map's choice, not a defect | the map declares field_values for the field |
<!-- lint:observations:end -->

### Conformance checks (D289)

Findings report on every write. In a git KB a write that introduces a finding it could have avoided is refused, and nothing switches that off or accepts the finding (D350, D907, `control-plane.md` §Commit per logical operation; the checks no write can avoid are listed above).

A write response carries these checks for the concept it wrote, plus the body, link and light graph checks `lint.ScopedCheck` runs without walking the KB (D312, `control-plane.md` §`concept_write`); the whole-KB structural checks stay with `lint` and `gate_check`.

Lint also compares a KB with the standard fields the server reads, not only with the contracts the KB declared. Like every finding they are problems (D947), and a check fires only on a field the KB actually has.

- `nonstandard_field`: a frontmatter key that is a known synonym of a standard field. Standard field absent: the finding carries the fix `rename_field <synonym> → <standard>`. Standard field also present: the finding says so and has no fix, since merging values is a judgement. A field the page's own map contract names (`required_fields` or `field_values`, for the page's type or `*`) is not reported: the contract is explicit, so the page keeps that name and the repair never renames it. Keys match case-insensitively. A `lint_ignore` key on a concept is reported here too, with the fix `drop_field`: D907 removed every way to silence a finding, so the key does nothing and the repair drops it. The synonym table (`StandardFieldSynonyms` in `internal/lint/conformance.go`):

  | Standard field | Known synonyms |
  |---|---|
  | `timestamp` | `updated`, `updated_at`, `last_updated`, `modified`, `date`, `aggiornato`, `data` |
  | `provenance` | `sources`, `source`, `refs`, `fonti`, `fonte` |
  | `status` | `state`, `stato` |
  | `description` | `summary`, `sommario` |
  | `tags` | `keywords`, `parole_chiave` |
  | `review_after` | `review_by` |
  | `superseded_by` | `replaced_by` |

  The `timestamp` synonyms are English words that are legitimate fields in their own right, so they are flagged only when the value is a scalar string that parses as `YYYY-MM-DD` or RFC 3339; `data: some payload` or a list value gets no finding and no fix. The other standard fields flag every synonym.

- `tool_param_field`: a key named like a parameter of a concept-write tool (`lint.ToolParamFields`: `id`, `frontmatter`, `body`, `if_match`, `template`, `vars`, `old_string`, `new_string`, `replace_all`, `edits`, `unset`, `frontmatter_append`, `frontmatter_remove`, `operations`, `op`), fix `drop_field`. The same write is rejected, see `docs/control-plane.md`.
- `missing_value_contract` (an observation, D947, on the map's `_map.md`): a scalar string field present in at least 5 concepts of the map, with at most 8 distinct values (no cap for `status`, the vocabulary every reader assumes) and no `field_values` for it (map-wide or for the dominant type). The message lists the observed values with their counts and the line to add, typed (`field_values.<Type>.<field>`) when at least 90 % of the carrying concepts share one type. `title`, `type`, `description`, `timestamp`, `review_after`, `superseded_by`, the free-form or list-shaped `provenance`, `tags`, `resource`, `secrets_source`, and the synonyms above are never suggested (D295). No fix: declaring a vocabulary is a judgement, so `kb_review` lists it (kind `observation`, the proposal in its `proposal` field) and `lint` does not.

- `malformed_frontmatter` (no fix, D295): a top-level key with a scalar value followed by indented `- ` lines. The stdlib-only parser (D8) keeps the scalar and drops the lines, so the value is silently truncated; the message names the key and the line.
- `stringified_list` (fix `listify_field`, D314, D357): a list field (`provenance`, `tags`, `related`, `open`, `secrets_source`) whose parsed value is a string, whatever it looks like: `"[a, b]"`, `"[a]; [b]"`, `"- a"`, but also the commonest shape, a bare `tags: "x, y"` or `tags: backup`. The parser reads a quoted flow list as a scalar, so nothing else sees it. `kb_repair` rewrites it as a real list: a written list splits as written; a bare string splits on commas only for the identifier fields (`tags`, `related`, `open`), while `provenance` and `secrets_source` become a one-element list with no split, since a source citation may contain commas (`"Talk, 2024"`).
- `title_h1_mismatch` (fix `sync_h1` or `set_value`, D315, D357): the frontmatter `title` and the body's first `# ` heading both exist and differ. The title is the label `concept_list`, search and the Atlas show, so the heading is normally the wrong one: `kb_repair title_h1_mismatch` overwrites it with `# <title>` (plain text: formatting in the old heading goes; only the first heading, never one in a code fence). When the title is the one `title_quality` rejects (decorative characters, over-long, a status word, a forbidden term) and the heading is not, the heading is the good side: the fix is `set_value title` with the heading's text. When both fail the predicate there is no fix (judgement). `title_quality` and this check share one predicate (`titleTextIssues`).
- `missing_title` (fix `set_value title`, D357): no title. The fix is the first heading when it passes the `title_quality` predicate, else, for a file name that is a slug, the name with `-` and `_` turned into spaces and the first letter upper-cased; a heading that fails the predicate and a non-slug name give no fix.
- `repeated_link` (fix `unlink_repeat`, D357): the same target linked more than once in one paragraph (a list item or heading is its own paragraph; code and images are skipped; `duplicate_link` covers text against "See also"). One finding per target; the fix keeps the first link in each paragraph and turns the later ones into their label text.
- `unknown_type` (fix `set_value type` when one known type matches case-insensitively, else judgement, D357): a `type` outside the KB's palette, the union of the types its `templates/*.md` declare and the `concept_types` of its strict maps. A KB with an empty palette (no template with a literal type, no strict map) is not checked. A type with no case-insensitive match is reported only where the page's map has adopted types (`concept_types`, so strict, or `templates`), and its message names the fix: declare the template that `kb_review kind=template_proposal` proposes. In a map that has adopted neither, an undeclared type is the KB's declaration to make and not a finding (D947, superseding D688): `template_proposal` proposes the template. The types the server keys on (`kb.IsKeyedType`, D853) are exempt when no declared type matches them in any case and the page's map is not strict; in a strict map that does not list one the finding stays, with the fix "add it to the map's `concept_types` (`map_update`), or move the page". A strict map checks against its own `concept_types`, not the palette: a type another strict map declares is still reported there, with the same fix (#896).
- `value_case_variant` (fix `set_value status` to the majority spelling, D357): a `status`, in a map whose contract does not constrain it (`invalid_field_value` owns a constrained one), that equals case-folded a different spelling used by more concepts KB-wide. A tie reports both sides and fixes neither.
- `unmapped_folder` (on the folder, fix `scaffold_map`, D357): a top-level `data/` folder that holds concepts and has no `_map.md` (nor legacy `_archive.md`): `map_list` shows it with no title or kind and no contract applies. The fix writes the descriptor `map_create` writes for `kind: map` (`type: Map`, `title` from the folder name, no contract; `index.md` and `log.md` only when missing), through the same KB function (`ScaffoldMap`). It repairs folders, not concepts: the response of `kb_repair` says `found_folders`, and repair-on-write never runs it.
- `stray_file` (no fix, D357): a non-Markdown regular file in `data/` outside any expanded concept (at the top of `data/` or directly in a map), neither junk (`junk_file`) nor hidden. Moving or deleting it is judgement, so it is a `lint_judgement` review item.
- `title_quality` (fix `set_value title` for the decorative-characters case only, D315, D366): the title carries a decorative character (Unicode symbol or modifier, emoji included), is longer than 100 characters (`defaultTitleMaxLength`, a constant: no map changes it, D907), holds a lifecycle word (`attivo`, `active`, `dismesso`, `deprecated`, `draft`, `superseded`, `preparazione`, `archiviato`, `archived`, `declassato`, whole words) while the concept has a `status` field, holds one of the map's `forbidden_title_terms`, or the concept's own slug starts `YYYY-MM` in a map that is not `kind: journal`. One finding per rule that fires; a KB that wants it stricter promotes nothing here, it fixes the titles. Only the decorative case has a fix (auto-repair safe, D366): the title without those characters and with the whitespace collapsed, when something is left; the heading follows through `title_h1_mismatch` in the same fixpoint. Length, status word, forbidden term and date slug are wording judgements and keep none.
- `mangled_placeholder` (no fix, D314): the body holds a `` `repo:key` `` or `` `path:key` `` code span followed within 40 characters by "between double braces" (or its Italian/French forms): a `{{…}}` placeholder an import unwrapped into prose. Fenced blocks and lines containing `{{` are skipped.

Two finding kinds gained a mechanical fix in D295. `broken_link` in the `index.md` of an expanded concept carries `rebase_link` (`field` the href, `to` the rewritten href) when the link resolves against the pre-expansion file `<id>.md`: that is the damage an expansion did before `concept_expand` rebased links. `duplicate_link` is one finding per repeated target, and carries `drop_link_item` (`field` the exact list line) when that item is a single link and nothing else: the link stays in the text, the item goes, and a section left with no item loses its heading — unless the page's template requires that H2 (by name or alias, and not among its optional sections) or a deeper heading follows it, in which case the empty heading stays (D827); a line of prose under a links heading is enough to keep the section, and the `duplicate_link` and `template_section_missing` messages say so. The links section ends at the first following heading of any level, so a `###` under it is text: its links are not the section's, and the repair never edits it (D827). An item with any other word keeps no fix — the word may be the reason. A `rebase_link` with an empty `to` (D310) is a link that resolves to the concept itself: the repair keeps the label and drops the link syntax. `index_link_form` (D310) flags a link spelled `<concept>/index` where the concept exists: a markdown href gets `rebase_link` to the relative `<concept>.md`, a wiki-link `rewrite_wiki_link` (`field` the old ID, `to` the new; alias and anchor are kept). `reciprocal_link_item` (D301) carries the same fix for a link-only item whose target links back to the concept from its text — a back-link only in the target's own links section does not count, so a mutual pair listed only in the two links sections is never flagged and repairing it can never drop the edge (D309): backlinks keep the edge navigable both ways, so the item is a second write for an edge the server already exposes. It is an efficiency choice the operator makes with `kb_repair reciprocal_link_item` (the heartbeat never runs it), not conformance debt, and refines D287 without reverting it: only reciprocated items go, the section stays. `map_misfit` names only a map whose contract admits the concept's type (a strict map's `concept_types`); with no admitting majority there is no finding.

### Value vocabularies (D296)

D289 converges field names; D296 converges **values**, starting with `status`. The server knows *families* of synonyms, never the canonical value: that is always the KB's — the one its contract declares, or the most frequent one observed. Values compare after folding case, accents, whitespace and `_` (`In corso` ≡ `in-corso`). The built-in families (`lint.ValueSynonymFamilies`) ship English plus the languages contributors add, and a map extends them in any language with `value_synonyms.<canonical>: [synonym, …]` in `_map.md` (or `map_update` `value_synonyms`); a KB in a language the table does not know gets no automatic answer until it declares its synonyms — never a wrong one.

| Family | Members |
|---|---|
| `done` | done, completed, complete, completato, completata, finito, finita, chiuso, chiusa |
| `resolved` | resolved, risolto, risolta, closed, fixed |
| `in-progress` | in-progress, in-corso, wip, ongoing, doing |
| `blocked` | blocked, bloccato, bloccata, on-hold, in-attesa, waiting |
| `proposed` | proposed, proposto, proposta |
| `draft` | draft, bozza |
| `active` | active, attivo, attiva, current |
| `deprecated` | deprecated, dismesso, dismessa, retired |
| `suspended` | suspended, sospeso, sospesa |

`open`, `decision-needed`, `reference`, `accepted`, `rejected`, `superseded`, `mitigated`, `monitoring` are deliberately in no family: they are distinct states, or KB-specific.

- The `missing_value_contract` observation on `status` folds each family onto its majority member and each prose value onto its leading token, and carries a structured `proposal: {key, field, type?, values, mapping}` next to the message. Declaring it is the operator's judgement (`map_update`); after that the per-concept findings are mechanical.
- `invalid_field_value` carries `set_value` when the value is, up to folding or by family, exactly one allowed value. Two candidates, or none, give no fix.
- `prose_value`: `status`, or a field the contract constrains, holds a sentence (a separator such as ` — `, `;`, `: `, or more than three words). It carries `split_value` when the leading token is a contract value or, with no contract, a family member: the field keeps the token and `kb_repair` writes the rest as `> <field>: <rest>` after the first heading, so nothing is lost.

### Invalid pages are lint checks (D356)

`validate` reports what the write path would refuse about a page; lint reports the same facts so the repair, the heartbeat, `kb_review` and the doctor see them. `validate` itself is unchanged.

- `missing_frontmatter`: no frontmatter block (an empty file included). Fix `add_frontmatter`: `type` is the resolved type, `title` the first H1, else the file stem with `-`/`_` turned into spaces. No fix when the type does not resolve.
- `missing_type`: a block with no `type` (delimiters and zero keys are this, not `missing_frontmatter`). Fix `set_value type` with the resolved type.
- `unparseable_frontmatter`: the block does not parse. Fix `quote_value`: only when exactly one line fails, the lines after it are plain top-level entries, and double-quoting that line's value (escaping `"` and `\`) makes the block parse with the same key set.
- `concept_too_deep`: more than three ID segments outside `services/`. No fix (where a page belongs is judgement): a `lint_judgement` item.
- `nonslug_file_name`: the file stem (the directory name of an expanded concept) is not `^[a-z0-9]+(-{1,2}[a-z0-9]+)*$`. Fix `move` to the slugified ID (`ß`/`ẞ` → `ss`, `æ`/`Æ` → `ae`, `œ`/`Œ` → `oe`, `þ`/`Þ` → `th`, `ĳ`/`Ĳ` → `ij` before lowercase accent folding; runs of other characters → `-`), applied through `concept_move`'s code path so inbound links are rewritten; `CrossConcept`, stage 2 of the heartbeat. No fix, and a message naming it, when the target exists.
- `empty_concept`: no non-whitespace body. No fix: nothing is ever deleted automatically; a `lint_judgement` item.

The four errors are evaluated by `Run` only (a write that would produce one is refused before it lints). **Type resolution**, shared by both type fixes: the type used by every typed page of the map, when there are at least two and they all agree; otherwise none (the map's `default_template` type comes first once a map declares one). A majority is not enough: a wrong `type` silently changes contracts. Pages without a type are neither votes nor disagreement.

**Repairs may write an invalid page as long as they do not make it worse.** `kb.RepairConcept` has `WriteConcept`'s path resolution, lock and stale guard, but accepts a write when the validation errors after are a subset of those before: a page with no `type` may be rewritten without one, so an unrelated `nonstandard_field` fix is no longer blocked by it; a page too deep keeps its path; a path that does not exist is never created. It backs `kb_repair`, the fixpoint applier and repair-on-write only. Every agent-facing write tool keeps `WriteConcept`'s full validation.

### Decay checks (D297)

Lint also sees a KB **decaying**: work never closed, closed work not finished, pages without the shape their template promises, open questions nobody counts, facets that stopped being facets. The checks below are problems like any other (D947), except `facet_sprawl`, an observation. A map contract tunes them with these keys, also settable through `map_update`:

| Key | Meaning | Default |
|---|---|---|
| `open_statuses: [...]` | statuses that mean "not finished" | the `in-progress`, `blocked`, `proposed`, `draft` families, `open`, `decision-needed`; never the `active` family, which means "the page is valid" in a journal and in a map alike (D321). `map_create` with `kind: journal` writes `open_statuses: [open, in-progress, blocked]`; a KB that reads `active` as open lists it |
| `open_field: <key>` | frontmatter key that carries the open/closed state of a concept of this map (D347); `open_statuses` lists its open values | `status` |
| `stale_after: <days>` | age after which an open concept is stale; a positive number of days (D907) | 60 in a journal; 30 in a map that holds work (`open_statuses` set, or a `status` vocabulary in `field_values` with an open value, D346); none in a reference map (a reference page is not stale by age). The default is computed at read time, never written to `_map.md`; `map_list` shows the effective value (`stale_after`, `stale_after_defaulted`) |
| `harvest_after: <days>` | age after which a closed journal entry is a `harvest_candidate` (D322); a positive number of days (D907) | 45 |
| `open_markers: [...]` | words that mark an open question, in the KB's language | `TODO`, `TBD`, `FIXME` |

Ageing cannot be switched off (D907): `map_update` with `stale_after` or `harvest_after` set to `-1` removes the key (back to the default above), and `0` is refused ("staleness cannot be switched off", "harvesting cannot be switched off"). A `0` already in a `_map.md` is a removed key (`contract_legacy_key`, §Maps and Journals) and reads as the default. A map that should not age its pages declares no `open_statuses` and is a reference map.

- `stale_open`: open status and `timestamp` older than the effective `stale_after` (`map_update` resets an explicit value with `-1`; `kb_status.stale_count` is a different signal, past `review_after`), unless the concept declares a `review_after` today or later: that suspends the timer (D321); a `review_after` before today does not, it makes the wait overdue (and `stale_claim` fires: both judge the date by UTC day, so neither treats the review day itself as past). `waiting_on` is a free-text field naming who or what blocks the work, with no vocabulary; the doctor sets both on a finding only the operator can resolve.
- `status_semantics` (D321): `status` in the `active` family on a concept of a journal whose `open_statuses` does not list it, which is checked on write too; never raised in a map with `open_field`, where `status` is the lifecycle (D347). The review kind `status_reclassify` proposes the replacement.
- `closed_with_open_items`: a status of the `done` or `resolved` family with unchecked `- [ ]` items outside code and outside a section whose H2 matches the map's `procedure_headings` (default `procedure`, `steps`, `how to`: a procedure's checklist is a reusable template, D313).
- `template_section_missing` (D352): required sections of the page's template (optional ones subtracted, an alias counts as the section) the page lacks, compared folding case and accents; headings inside fenced code are ignored. Evaluated on every template-bound page of a map that declares templates (below); closed pages are exempt. No fix: an empty heading is a hollow promise.
- `open_marker`: marker occurrences outside code, outside heading lines, outside table rows of a concept in an open phase (D313), and outside struck-through `~~text~~` (closed or cancelled, D307), whole words, case- and accent-folded; `kb_status.open_markers` totals them as `{concepts, markers}`.
- `facet_sprawl` (an observation, D947, on `_map.md`, directory-level): `tags` with at least 30 distinct values, half or more used once; the message lists the ten most used as the likely vocabulary.

### Templates are page schemas (D352)

A page names the template it follows in the frontmatter field `shape` (a template slug; `shape` is a page field, never a tool parameter, unlike the `template` argument of `concept_new`). A map declares which shapes it accepts, and a template is a **closed schema**. Nothing below is evaluated, and no template is read, for a map that sets neither `templates` nor `default_template`. A map that declares either binds **every page** to one of its templates (D371, D907): there is no key that opts a page or the map out, and every bound page must carry its template's required sections.

| Map key | Meaning |
|---|---|
| `templates: [slug, ...]` | the shapes the map accepts (templates are a KB-wide library: several maps may share one, one map may hold several for one `type`) |
| `default_template: <slug>` | the one a page of the template's `type` follows when it names none; must be in `templates` (else `contract_malformed`) |

`require_template` and `template_sections` no longer exist (removed keys, §Maps and Journals): declaring `templates` or `default_template` is what makes a map strict.

A page's template resolves in this order: its `shape` when it names an existing template; else the map's `default_template` when the page's `type` is that template's type; else `templates/<lowercased type>.md`; else none. An unresolvable `shape` falls back to the last two for the section checks, so a typo does not hide missing sections.

A template declares its schema with flat dotted keys in its own frontmatter (a nested block stays opaque to the parser); `artifact_write` refuses an unknown `x-template.*` key, a wrong value kind, a placeholder in metadata, an optional section or alias canonical that is not an H2, and an alias that is another section's name or listed twice. `concept_new` strips every one before rendering and stamps `shape`; `template_list` returns them.

A template variable can also take a list of strings from `concept_new` (D715): write `key: {{name}}` or a list item `- {{name}}` / `[a, {{name}}]` as the whole value; the caller's value decides the kind, a list anywhere else is refused. `{{timestamp}}` is the one variable with a default, today's date (`YYYY-MM-DD`, UTC), used when the caller passes none (D830).

A template's sections are its H2 headings outside fenced code or, when it has none, the H2 headings of its first fenced block: guidance prose followed by a fenced `markdown` skeleton is a supported shape (a fence with no H2 heading is not a skeleton). `concept_new` reads the template through the same scan: for a skeleton it renders the fence's content as the page body and drops the prose around it (D830), so the page carries the sections as real headings; any other template renders its whole body. The variables of a template are those of its frontmatter and of the body it renders.

| Key | Meaning |
|---|---|
| `x-template.description` | one line |
| `x-template.required_fields` / `optional_fields` | fields the page must carry (non-empty) / may carry |
| `x-template.field_values.<field>` | allowed values; must be a subset of the map's `field_values.<field>` (else `contract_malformed` on the map) |
| `x-template.optional_sections` | H2 names a page may omit; the template's H2 headings, in order, are its sections, required = all minus these |
| `x-template.section_aliases.<Canonical H2>` | accepted alternative names |
| `x-template.open_sections` | `true`: H2 headings the template does not name are allowed (default `false`) |

On a map that declares `templates` or a `default_template`, all evaluated on the write path: `template_missing` (no `shape`, nothing resolves; fix `set_value shape` when exactly one template of the map has the page's type, or the `default_template` when the page has no type), `template_unknown` (`shape` not a slug, not a string, or no such file), `template_not_allowed` (the resolved template is not in `templates`), `template_type_mismatch` (a bound page's `type` differs from its template's: `shape` is the specific choice, `type` follows it; fix `set_value type`), `template_field_missing`, `template_field_value` (fix `set_value` when one allowed value matches after folding), `template_extra_section` (an H2 that is no section, optional section or alias; merge its text into a section or move it, never delete it), `template_section_alias` (fix `rename_heading` to the canonical name, none when the page has both), `template_section_order` (fix `reorder_sections`), and `template_section_missing` (above). Closed pages are exempt from `template_section_missing`, `template_field_missing` and `template_extra_section`; the binding checks still run. A map with no `templates` is not a finding (D947): `kb_review`'s `template_proposal` proposes the ones its pages already follow.

`reorder_sections` moves whole H2 blocks into template order, keeps extra sections after the last template section in their relative order, keeps the text before the first H2 first, never treats a heading inside fenced code as a section, and normalises the blank lines between blocks; `rename_heading` rewrites one heading line. Neither writes section text. `template_missing`, `template_type_mismatch`, `template_field_value`, `template_section_order` and `template_section_alias` are among the safe repairs the heartbeat applies (`lint.AutoRepairOrder`), in that order (a reorder lists the headings as written, so it runs before the rename).

**Induction.** `kb_review` kind `template_proposal` (ranked after `duplicate_candidate`) is one item per template slug (D720), built from every page that would bind to it across the maps that would declare it: a type with at least three pages in a map with no `templates`, or with most of them unbound. Sections are the H2 headings (folded, whitespace collapsed) present in at least 60 % of those pages, ordered by median position, optional from 50 % and at least two pages; spellings of one folded heading become aliases. Fields are required from 90 % of pages, optional from 50 % and at least two. Values are proposed when the map already constrains the field, or when there are at most eight, one repeats and every map gives the same list; a field of identifiers (every value a URL, or one value per page) is never a vocabulary. The item carries `template_slug`, the ready `template` text (empty when `templates/<slug>.md` exists) and the `map_update` arguments (`templates`, `default_template`); a proposal spanning several maps carries one map_updates entry per map instead. The server never writes it (D14): the doctor does, with `artifact_write`. Nothing dismisses the item (D907): it closes when the map declares the templates.

### The `archived` status (D322)

`archived` is a server-reserved status, after `done`: the entry is finished **and** its durable facts have been carried into the live pages it links. It is retired like `deprecated` and `superseded` (`link_to_retired` counts live linkers, `map_misfit` and `link_suggest` skip it; a `digest`-typed page linking archived entries is not a linker) and closed, never open, and in no synonym family: it is a lifecycle stage, not a value a KB chooses among synonyms. An archived concept stays readable and linkable but leaves the active surface: default `search` (with an archived fallback, `docs/control-plane.md`), `read_cost`, the `atlas_overview` structure graph and the `map_oversize` count. The other graph tools still show it; `deprecated` and `superseded` concepts still count in `read_cost`. The bundled `kb-doctor` skill does the harvest: facts into live pages with a dated link back, `status: archived` with a header line, one `reference` digest per quarter. Nothing is deleted or moved. The status is allowed on any concept, not only in a journal: it is the harvest outcome for journal entries and also what `concept_archive` (D344) writes into the archive map, so no check flags it elsewhere. A retired page that stays in a live map gets no `index_incomplete` or `orphan` finding (D826). Where a retired concept went, when and why is not in the page: `concept_archive` and `supersede` log it (`concept_archive: <src> -> <dst> (<status>[, <reason>])`, `supersede: <src> → <dst>`) and `kb.Retirements` reads the root `log.md` back (the reason is one line: `concept_archive` collapses line breaks, and the parser also reads an older multi-line title), which is how `GET /api/ui/v1/kbs/{kb}/retired` (D820), and the Atlas's Retired tab on it, tell the story of a page archived before it existed.

### Review keys (D298)

`kb_review` (`docs/control-plane.md`) builds the doctor's work list from the graph, the lint findings and the search index. Three contract keys, also settable through `map_update`, tell it where the KB keeps what the server must not guess:

| Key | Meaning | Default |
|---|---|---|
| `promote_to: <map>` | the map a journal's reusable procedures belong in: a concept with 5+ consecutive numbered items, or an H2 matching `procedure_headings`, and no link into it is a `promotion_candidate` | none: no promotion candidates |
| `procedure_headings: [...]` | H2 prefixes that mark a procedure, in the KB's language, matched case- and accent-folded | `procedure`, `steps`, `how to` |
| `glossary: true` | this map is where the KB defines its terms: a term used in any of its concepts is never a `glossary_gap` | off: only `glossary.yaml` defines terms |
| `work_map: <map>` | the existing map where this map's work belongs (D302): a concept here with unchecked items or an open-phase status and no link into it is `scattered_work`; a name that is no map is `contract_malformed` | none: work may live anywhere |

**Built-in, not configurable (D345).** `duplicate_candidate` skips period series and `zombie_work` reads only origin/dependency sections, whose headings are a fixed English and Italian list (no contract key: the `map_update` catalogue budget, D285). A KB in another language writes its origin section under one of those headings, or retargets the link; a duplicate pair is closed by linking each page to the other (D854), which keeps later pairings of either page visible.

**Work items (D302).** A work item is a concept in an open phase for its map (`open_statuses`, else the defaults; never `active`, D321; the `draft` family only where `open_statuses` lists it, since it marks a page still being written), of any type, or an unchecked `- [ ]` item outside code in any concept, whatever its status. With `open_field` the state is that field's value, falling back to `status` for a concept without it; `archived` still closes, and any other non-open value is closed (D347); `work_list` entries then carry `state`. A concept is also work by what it is, whatever its status (D948), and then carries a `reason` in `work_list`: `contradiction` (an open `Contradiction`: no `resolution_status`, or `open`), `gap` (an open `Contradiction` of kind `missing_context` or `open_question`, D273) or `review` (a page whose `review_after` has passed, unless it is open by its status already or finished: a done or resolved status, `archived`, `deprecated` or `superseded`, which have nothing left to check); it has `open_phase: true`, counts in `open_concepts` and `by_reason`, and not in `by_status`. `work_list` returns them read-only; staleness is `stale_open`'s threshold, extended to a concept whose unchecked items are that old.

**Nothing dismisses a review item (D907).** There is no `lint_ignore`, no state kept outside the KB: `kb_review` lists what the pages are, and an item goes when the page state it asks for is written, in the same commit that explains why. `zombie_work`: close the page or retarget the link to the retired concept. `duplicate_candidate`: merge the pair, or link each page to the other (D854). `harvest_candidate`: harvest the entry, then archive it (D322). `lint_judgement`: fix the page until the check no longer reports it. `repeated_fact`: keep the fact in one concept and link to it. `read_hotspot`: split the page. `glossary_gap`: define the term in `glossary.yaml` or in a `glossary: true` map. `map_naming`: give the maps' titles one shape, or a poor title a better one. `status_reclassify`: set the status the item proposes. `scattered_work`: create the concept in the `work_map` and link the page to it. `promotion_candidate`: create the procedure in `promote_to` and link it. `template_proposal`: declare the templates in the map. `observation` (D947): the page state the generated table in §Observations names for its `check`: split the page, link the group in, move the page, declare the vocabulary.

A finding whose remedy is mechanical carries `fix: {kind, field, to?}` (`rename_field`, `drop_field`, `rebase_link`, `drop_link_item`, `rewrite_wiki_link`, `set_value`, `split_value`, `sync_h1`, `unlink_repeat`, `scaffold_map`, `retarget_links`, `drop_index_entry`, `add_frontmatter`, `quote_value`, `move`); the rest carry none. `lint.CheckConcept` computes the frontmatter-driven checks of one concept without walking the KB (`missing_required_field`, `invalid_field_value`, `forbidden_field`, `nonstandard_field`, `tool_param_field`, `missing_title`, `title_h1_mismatch`, `title_quality`, `machine_path`, `stale_claim`); `Run` calls the same function, and the write tools return its result.

`machine_path` has no allow-list a map can extend (D907): a client-local path (`/Users/…`, `/home/…`, `~/…`, `C:\Users\…`) outside a URL is a finding, apart from the fixed tool paths above, and the page is fixed by writing `{{repo:<key>}}` or `{{path:<name>}}` (declared in `paths.yaml`, §The path placeholder registry) or `$HOME` where the path is the reader's own. The finding names every path it found and has no auto-fix: which placeholder a path stands for is the author's call.

The two "too big" numbers are related, and both are constants (§Cost thresholds): the `concept_oversize` observation fires at **half** the size (30,000 bytes) at which `concept_read` degrades to an outline (60,000), so the observation appears in `kb_review` before reads change shape (it is never raised at write time), and the
message names both bounds. For a satellite the message does not advise `concept_expand` — the write
path caps depth at three segments, so that remedy is structurally unavailable — and names splitting
into sibling satellites instead.

### Artifact checks (D316)

Lint also covers what a KB ships beside its concepts. The KB-level ones run
only in a whole-KB lint, and their findings name the artifact file (`skills/<name>/SKILL.md`,
`agents/<name>.md`, `instructions.md`), never a concept.

- `artifact_unused` (D1043): a skill or agent never invoked since it was added, or last used more
  than `usage_stale_days` ago, by the clients' usage reports (`.cartographer/usage.json`, below).
  The fix is a person's (delete it, or fix its description so a client selects it), so there is no
  fix kind and a write is never refused for it. A KB with no usage reports yields nothing; "never"
  needs the artifact's first commit to be older than the threshold (no history, no finding); a
  catalogue load is not a use. Whole-KB lint only, and the lint cache stamp includes `usage.json`
  and the UTC date.
- `skill_invalid`: `skill.Validate` would refuse the skill (unreadable frontmatter, name
  rules, name ≠ directory, no description), or a `skills/<dir>` has no `SKILL.md`.
- `skill_warning`: `skill.Validate`'s warnings (description over 1024 characters, body over
  500 lines).
- `legacy_tool_name` (fix `strip_tool_prefix`): a pre-D288 prefixed tool name
  (`<kb>__search`, e.g. kb_a__search) in a skill, agent or `instructions.md` (Markdown, text, scripts and config
  files; stylesheets are skipped, a BEM class has the same shape). A client's own MCP tool name,
  `mcp__<server>__<tool>` (Claude Code, Codex), is never flagged: it is current, not a prefix. The message names the mounted KB
  the prefix most likely meant, so the agent adds `kb: "<name>"`; the fix only strips the prefix.
- `skill_broken_ref`: a `tools/`, `scripts/` or `skills/` path in a skill's code (fenced
  block or inline code — prose is not scanned) that exists neither under the KB root nor in the
  skill's directory. The MCP methods `tools/list` and `tools/call` are protocol, not paths.
- `skill_git_command`: a skill's code runs `git add|commit|push|pull|merge|rebase|reset|checkout|stash`;
  the KB is written only through MCP tools. `git clone`, `status`, `log` are reads. A step that
  targets another repository names it, `git -C <repo> pull`, which is not matched: that is how the
  finding closes, since nothing accepts it (D907).
- `missing_instructions` (KB-level, no path): more than 10 concepts and no `instructions.md`.
- `junk_file`: a junk file git tracks, or would add at the next commit (`git ls-files
  --cached --others --exclude-standard`), outside an expanded concept's assets; remove it with `git
  rm`. `junk_asset` covers the assets (§Assets).
- `symlinked_path` (D1025, unscoped runs only): a symlink anywhere under `data/` or `services/`, found by walking the disk. Concept paths never follow a link: `concept_read`, `concept_write`, `concept_delete` and the locate step of move/expand refuse a symlink on the path (a map directory, the file, an expanded `index.md`, a dangling link included) with `invalid_path`, and the graph walk does not list a symlinked `.md`, so the concept is simply absent. Remove the link with `git rm`. No write can create one, so the check is not avoidable at write.
- `sops_format_mismatch` (also per concept): a `sops decrypt` (or `-d`)
  piped to `jq` or `python3 … json.load` without `--output-type json` in code — sops prints YAML by
  default. `sops_missing_file` (same scope): with a `secrets/` directory in the KB, a
  `sops decrypt secrets/…` naming a file that does not exist. Nothing is decrypted or read.
- `cross_kb_path`: an artifact hard-codes a sibling KB's root (or
  `~/cartographer-data/<name>`). The server injects the siblings' roots when it mounts more than
  one KB (`kb.KB.SiblingRoots`); a single-KB server has none and pays nothing.
- `skill_missing_perimeter`: `instructions.md` declares `perimeter` and a skill's
  description does not contain it (case-insensitive substring, so `ops` matches `DevOps`).
- `legacy_path` (per concept, fix `replace_prefix`): a concept body contains
  a prefix declared in `instructions.md` `legacy_paths`. One finding per prefix found; the repair
  rewrites every occurrence, all prefixes in one pass, longest first.

The Atlas Artifacts panel shows these findings beside each artifact (`docs/deployment.md`).

## Artifact usage (`.cartographer/usage.json`, D326)

`<root>/.cartographer/usage.json` holds the usage clients reported for the KB's skills and
agents: one row per (artifact, provider) with `last_used` and `count`. Like the rest of
`.cartographer/` it is **local state, never committed** (excluded through `.git/info/exclude`)
and not replicated between servers: "is this skill used?" is a question about one operator's
machines. Losing it loses nothing durable; the next `sync` of each client rebuilds it. On a data
dir that does not survive a restart, `kb_status.usage` reports `no_data` with `no_report_since`
(the server's start) until then (D333, `docs/deployment.md` §State and volumes). It feeds
the `usage` section of `kb_status` and the Atlas Artifacts panel's "Last used"
column, and lint reads it for `artifact_unused` (§Artifact checks, D1043). Written only by `POST /api/usage` (`docs/control-plane.md` §Usage reports).

## The path placeholder registry (`paths.yaml`)

A KB may declare, in `paths.yaml` at its root (beside `instructions.md`), the
`{{path:<key>}}`/`{{repo:<key>}}` keys its concepts and artifacts cite (D263):

```yaml
paths:
  claude-home: {description: Claude Code's per-user directory, default: ~/.claude}
repos:
  kb-tools: {description: the tooling repository, remote: gitlab.example.com/team/kb-tools, default: ~/src/kb-tools}
```

Keys are lowercase-hyphenated slugs; `description` is required; `default` is optional and must be
`~`/`$HOME`-anchored (an absolute or relative path, or a `..` segment, is refused); `remote` is
optional on repo keys only, a clone URL or `host/owner/name`, stored normalized; `bind` is optional
on repo keys only, a boolean: a repository a KB declares is bound to that KB for every provider in
workspace scope (only with a `remote:`, which the clone's origin must match: the file is unsigned), and `bind: false` keeps the placeholder without binding it (D1019, `sync.md`
§Workspace scope), for a repository the KB only cites. It is written with
git or `artifact_write` (validated strictly: any malformed entry refuses the write, naming it) and
read tolerantly (a malformed entry is left out). It is not a concept and is never materialized on a
client: `sync_pull` serves it as data, and the client uses it to resolve keys
(`docs/sync.md` §The KB's placeholder registry).

When the file exists, `lint` adds three checks — none when it does not, so an existing KB is not
flooded on upgrade:

- `unknown_placeholder` (per concept): the concept cites a key not declared
  under the matching kind. One finding per concept, naming every undeclared key; the keys come from
  the graph cache's placeholder facet.
- `missing_registry` (KB level, D314): placeholders are cited but `paths.yaml` does not exist. One finding on `paths.yaml` with the count; creating the file closes it. A `paths.yaml` that exists but is unreadable gives `contract_malformed`, not this.
- `unused_placeholder` (on `paths.yaml`, whole-KB lint only): a declared key no concept and no
  artifact (`skills/`, `agents/`, `hooks/`, `mcp/`, `instructions.md`) cites.
- `hook_invalid` (on `hooks/<name>/hook.json`, whole-KB lint only): a hook whose `hook.json` `artifact_write` would now refuse (invalid JSON, missing `event`/`command`, a command outside the hook's directory) or warn about (an event outside the declared vocabulary, which never fires if misspelled), so hooks written before that validation surface. Independent of `paths.yaml`: it applies to every KB with a `hooks/` directory ([D284](decisions/D284-hook-json-is-validated-against-one-declared-event-vocabulary.md)).
- `contract_malformed` (on `paths.yaml`): a malformed entry, or a file that is not a YAML
  mapping at all — in which case no key is reported undeclared.

`machine_path` also names the declared key whose `default` is a prefix of the flagged path, e.g.
`~/.claude/settings.json` → "use `{{path:claude-home}}/settings.json`". It stays one finding per
concept but names every other disallowed path in it ("and 2 more here: …"), without the prose
punctuation that follows a path, so fixing the first is not how the author learns of the next.

## The KB's own settings (`settings.yaml`)

How a KB behaves lives in the KB, not in the server that mounts it (D746): five people
mounting the same KB get the same behaviour. `settings.yaml` at the KB root, beside
`paths.yaml`, is the **only** place a KB's policy is set (D906): the server's `kbs[]` keeps just
`name` and `remote`. It carries two keys, and the write gate, the safe repairs and the doctor's cadence are not among them (D907):

- `usage_stale_days`: days a skill or agent may go unused by every client before `kb_status.usage` counts it as stale
  (and `artifact_unused` flags it in Health, D1043). Default 42, minimum 7, and no value turns the signal off: a value under 7 is raised
  to 7 with a finding (`kb_status` `settings_findings`, a startup warning), and `0` or a negative
  number is read as the default (the realignment resets a `0` by dropping the key);
- how changes land: `branch`, the branch Cartographer commits and pushes to, in place of the
  remote's default one (D335, D903). It has no `kbs[]` form: it is a decision of the team that
  owns the KB, the same for every server that mounts it. It is read **only from the
  `settings.yaml` committed on the remote's default branch** (the working copy's file is parsed
  and shown, but changes nothing until it reaches the default branch), the first push creates the
  branch when the remote lacks it, and a name git would not accept is a finding.

Two things are derived and have no key (D906). A KB **declares secrets** when at least one YAML
file directly under `secrets/` carries sops metadata (read, never decrypted): a mount of such a KB
without an age key reports `capabilities.secrets: missing key` and warns at startup, instead of a
silent `disabled`. A KB's **MCP descriptors** (`mcp/<name>.json`) are always distributable: no list
names them; each client still approves a descriptor before installing it (`docs/sync.md` §MCP
servers).

Names, values and defaults are in `config.example.yaml`'s KB sections and in the file `kb create`
writes (`cartographer config --all` lists them). The keys bound to one machine (`remote`, `name`) stay in the server's `kbs[]`; naming one in
`settings.yaml` is a finding. So is a removed key: the server git profile's `git_profile`,
`git_base_branch`, `git_forge`, `github_*`, `git_working_branch`, and `git_branch`, which became
`branch` (D903); `author_*` and `committer_*`, since the writer is the author and the committer, and the
server file names the identity of what has no writer (D904, D950); `ssh_key`, `known_hosts`, `sops_age_key_file` and `artifact_signing_seed`, whose
files are in the server's credentials directory, since a secret never belongs in the KB (D905);
`allow_artifact_write` and `allow_template_write`, since artifact writes are always allowed, and
`mcp_allowlist` and `secrets`, derived as above (D906); `write_gate`, `auto_repair`, `repair_on_write` and the `doctor_*` keys (D907), since the gate refuses what a write could have avoided in every git KB, every safe fix is applied on write, by the background repair every day and by `kb repair --apply`, a doctor session is proposed 14 days after the last one and its review budget (10) is a parameter of the session. Each is ignored with a finding that names what to use, and
dropped when the server realigns the file; `git_profile: server`, which cannot be honoured, makes
the KB refuse to mount. The realignment is one commit by the server, through the normal write
path, so it is revertible (`docs/deployment.md` §Upgrading: realignment); a KB created by
`kb create` starts with no key set.

**One home (D906).** The effective value is the KB's `settings.yaml` if it sets the key, else the
default. A key of the server's `kbs[]` that is a KB's setting (`usage_stale_days`, …) is a removed key there: it is ignored with a warning that names the KB file,
and dropped when the server realigns its file (`docs/deployment.md` §Upgrading: realignment). The
origin of a value that comes from the file is `kb` in `cartographer config` and the Atlas settings
page. There is no local override: an operator who disagrees with a KB's policy changes the KB or
does not mount it. A KB without the file runs on the defaults, on every server; a discovered KB
reads it like a configured one.

The parser is tolerant, like `paths.yaml`'s: invalid YAML ignores the whole file (one finding);
an unknown key, a server-only key or an invalid value ignores that entry (one finding each,
`contract_malformed` on `settings.yaml`, and `kb_status` `settings_findings`).

The server re-reads the file when it changes: after a pull that moves HEAD and before a write
(`KB.RefreshSettings`). The per-KB policy is one immutable `kb.Policy` snapshot replaced whole (`KB.SetPolicy`) and read through `KB.Policy()`, so a refresh never races a reader and a tool call that takes one snapshot never mixes two policies (D775).

No MCP tool writes `settings.yaml` (`artifact_write` does not whitelist it): it changes the
behaviour of every mount, so it goes through git review like a Map contract. `kb create` writes
it commented out, rendered from the schema. The server itself rewrites an existing file only to realign
it (D903): at mount, when it holds a removed key, in one commit of the server's, and never when the file has
uncommitted edits.

## The glossary (`glossary.yaml`)

A KB may declare its terminology in `glossary.yaml` at its root ([D276](decisions/D276-a-kb-glossary-expands-search-and-flags-forbidden-terms.md)):

```yaml
terms:
  - canonical: Home Assistant
    aliases: [HA, hass]
    forbidden: [HomeAssistant]
```

`canonical` is required; `aliases` and `forbidden` are optional lists; no other key is accepted.
Every term is a non-empty string of at most 80 bytes. Terms are compared lowercased and
diacritic-folded (the folding `search` uses, D246): the same folded string may not belong to two
canonicals, nor be both an alias and forbidden. Like `paths.yaml` it is written with git or
`artifact_write` (strict: any malformed entry refuses the write, naming it), read tolerantly (a
malformed entry is left out, the earlier term keeping a contested string), and never materialized on
a client.

- **Search expansion.** A query containing a canonical or an alias as a whole-word phrase also runs
  with that phrase replaced by each other member of its group — forbidden terms never expand. At most
  8 queries run, the original first, and a variant is never re-expanded; hits are merged by id keeping
  the best score. The response names the variants in `expanded_to`, and a miss is recorded only when
  every variant came back empty. A KB without the file searches exactly as before.
- **Lint.** `forbidden_term` (per concept): the body uses a forbidden term as a whole word outside fenced and
  inline code (the masking the link graph uses, D150): a migration note quotes an old name in inline code. One finding per distinct term, naming the
  canonical. A malformed entry is `contract_malformed` (on `glossary.yaml`, whole-KB lint only).

## Extended concept types

`Service`, `Contradiction`, `Source` and `digest` are the types the server keys on (D853,
`internal/kb/keyedtype.go`): `Source` and `Contradiction` match exactly, `Service` (D158) and
`digest` (D322) in any case. `unknown_type` does not report them outside a strict map, with or
without a template, so the page `source_register` writes is never drift.
`validate()` enforces the normal frontmatter/layout rules and, in a strict Map (one with a non-empty `concept_types`, D907),
that the type is present in the Map's `concept_types` palette. It does not
validate a nested per-type grammar.

- A `Service` commonly carries flat fields such as `kind`, `base_url` and
  `secrets_source` or `secret_refs`; secrets may be owned by any concept, not
  only a Service. See [skills, services and secrets](skills-services-secrets.md).
- The contradiction tools use `resolution_status`, `contradiction_kind`,
  `involves` and `reason`.
- **Knowledge gaps (D273).** A `Contradiction` whose `contradiction_kind` is
  `missing_context` (the KB lacks information it should have) or `open_question`
  (a question raised and not yet answered) records something *unknown* rather
  than two claims that disagree. It has the same lifecycle (`resolution_status:
  open` → `resolved`, closed with `conflict_resolve`), but it never blocks
  `commit_gate` or `gate_check`, whatever its `involves`, and `involves` is
  optional (a question may concern a page that does not exist yet). `kb_status`
  counts open gaps apart from open contradictions (`open_gaps`).
- **The source ledger (D278).** A `Source` records one primary source the KB has
  absorbed (document, transcript, ticket, thread, web page, dataset), so the KB
  can answer *already ingested?*, *what is pending?*, *where does this claim come
  from?* and *what depends on this source?*. It lives in a journal (`sources` by
  default, created with `map_create(name: "sources", kind: "journal")`;
  `source_register` never creates it). Frontmatter: `title`, `source_kind` (free
  string), optional `locator` (a URL or a `{{path:<key>}}` placeholder, never a
  machine path), optional `sha256` (lowercase hex of the original bytes, computed
  by the agent), `timestamp` (the source's own date), `ingest_status`
  (`pending` \| `ingested` \| `skipped`) and `ingested_at`. The body is the
  agent's distillation (key facts, decisions, open questions); keeping the
  original is optional, as an asset of the expanded concept (D106, D270) — the
  server never fetches or copies anything. **Citation:** a concept cites a source
  by listing its ConceptID in `provenance`; an entry that is an existing Source ID
  is a source citation, anything else (URLs, free text) stays a plain note.
  **Dedup** (`source_register`): `sha256` decides when both sides have one,
  otherwise an exact `locator` match; a title match is not a duplicate. Marking a
  source ingested is a plain `concept_patch` of its frontmatter. `lint` reports an
  `ingested` Source nobody cites as `source_uncited`;
  `concept_delete` of a cited Source needs `force: true`; `kb_status` counts
  sources by status (`sources`).

Concept references are path-based. There is no separate immutable UID layer,
so use `concept_move` with backlink rewriting when an ID changes.

## Multi-concept refactors

A refactor that touches several concepts at once (renaming a shared field,
realigning summaries and companion pages) has three server-side primitives,
chosen by scope: `concept_patch`'s own `edits` array batches several edits
against **one** concept in one commit (D76); `concept_move` batches renames
with backlink rewriting across the whole KB (D72); `concept_batch` batches
`write`/`patch` operations across several **distinct** concepts as one
atomic logical operation — one commit, one summary `log.md` entry, and a
full rollback of every already-written file if any later step (including an
index update) fails (D125). Sequential `concept_write`/`concept_patch` calls
remain the right tool for independent, unrelated edits; `concept_batch`
exists so an interrupted multi-page refactor cannot leave a partially
realigned KB.
