# Data Plane — the Knowledge Base model

The data plane is the **source of truth**: UTF-8 `.md` files with YAML frontmatter, organized in a fixed hierarchy, versioned in git. The Go server holds no critical state: everything can be rebuilt from the files.

## Hierarchy

| Level | Name | What it is | OKF mapping |
|---|---|---|---|
| 1 | **Atlas** | A self-contained knowledge base; an instance hosts one or more | OKF bundle = git repository |
| 2 | **Map** / **Journal** | Map: a thematic domain with mixed `concept_types` (e.g. `smart-home`, `infra`). Journal: a chronological, append-oriented log (e.g. `incidents`, `notes`) | Top-level subdirectory, described by `_map.md` (`kind: map\|journal`) |
| 3 | **Concept** | A single knowledge page | `.md` file with frontmatter |

There is no intermediate categorization level (D77): category navigation is the job of curated `index.md` files, `search`, and the graph — not the filesystem. A growing concept becomes an **expanded concept** — a *state*, not a level: `concept_expand` turns `map/name.md` into `map/name/index.md` **without changing the ConceptID** (ID resolution tries `<id>.md` and then `<id>/index.md`, so no backlink breaks), and from there the concept can grow with `map/name/child` satellites and assets. Expansion is the prerequisite for owning assets. Expansion is also allowed in journals (e.g. a heavy incident with attachments). `concept_collapse` is its inverse (D160): `map/name/index.md` becomes `map/name.md` under the same ConceptID, so no inbound link changes. It refuses while the directory still holds satellites, assets or anything else besides `index.md` (a hidden file) — none would have a home after the collapse — and names them. `concept_merge` folds a satellite into its own parent, rebasing the merged body's relative links and redirecting every inbound link, including those from sibling satellites; both are `advanced`, i.e. callable by name but not advertised in `tools/list`.

Depth is **enforced on the write path** (D72 WP4): a ConceptID under `data/` has at most 3 segments (`map/concept/child`, where the third segment only exists inside an expanded concept); deeper writes are rejected. Reads are unaffected (legacy KBs remain readable). If a write implicitly creates a new expansion directory (e.g. `concept_move` into a nested path), the server also generates the `index.md` stub (`type: Index`, title from the name) — so `index_get`'s progressive disclosure never breaks. Lint defends the semantics of the hierarchy (D77 WP4, `concept_oversize` D78): `expanded_missing_index` (a directory with no `index.md`), `expanded_ambiguous` (both `<id>.md` and `<id>/index.md` exist: writes are blocked until one form is removed), `expanded_as_category` (many children not linked from the concept's index: the directory is being used as a taxonomy), `map_oversize` (a map with more top-level concepts than its threshold, D313: a thematic split is preferable to a subfolder; satellites of an expanded concept and every journal entry are not counted, a journal never fires), `legacy_archive_descriptor` (a pre-D77 `_archive.md` descriptor), `concept_oversize` (a concept beyond the byte threshold: a candidate for `concept_expand` into a dossier).

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
└── glossary.yaml                      # optional: canonical terms, aliases, forbidden forms (D276)
```

`services/` is included in `WalkConcepts` (search, graph, lint all see it) but its root is `kb.Root`, not `kb.DataRoot()`. Service concept IDs carry the `services/` prefix. `ResolvePath` is the one place that picks the root for an ID, and every operation that turns an ID into a file — read, write, collision check, removal, `concept_move` — goes through it (`LocateConcept` for callers that move files themselves). For the same reason `services` is not a valid map or journal name: `map_create` refuses it, since the scaffold would land under `data/services/`, where no read looks (D269). A `data/services/` left by an older version is neither read nor cleaned up. `agents/` and `hooks/` are not concepts (no OKF frontmatter, they don't go through `WalkConcepts`): they are provisioning artifacts materialized client-side — see `docs/sync.md` §Agents and hooks.

`templates/` is outside `WalkConcepts`: templates have no ConceptID and are never indexed, linted or added to the graph. A template is a KB-only artifact, not a provisioning kind: it is maintained through `artifact_*`, discovered with `template_list`, and used once by `concept_new`; it never affects a provisioning manifest or its revision.

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
lint_accept:                   # artifact path (or "dir/" prefix) -> checks accepted there
  "skills/deploy/SKILL.md": [skill_git_command]   # pulls the app repo, not the KB
---
```

`lint_accept` is how an artifact finding is accepted (D332): a skill's or agent's frontmatter is
read by every client, so the operator's judgement lives in the KB's own file, keyed by the path
the finding names. A key ending in `/` covers every file under it. It may name the artifact checks
reported on a file plus `sops_format_mismatch`, `sops_missing_file` and `legacy_path`; never an
error, `junk_file`/`junk_asset` (delete them) or `missing_instructions`. A name it cannot accept,
or one that matches no finding on its key (a stale entry), is reported as `lint_ignore_invalid` on
`instructions.md`. Write it with `artifact_write`, with the reason in the call's `reason` or a YAML
comment.

`paths.yaml` is likewise KB-only data, not a concept and not a provisioning artifact: the KB's
declared placeholder vocabulary, maintained through `artifact_*` or git and served to clients by
`sync_pull` (§The path placeholder registry below). `glossary.yaml` is KB-only data too, read
only by `search` and `lint` on the server (§The glossary below).

### Assets

An **asset** is a regular, non-Markdown file inside an expanded concept directory: a CSV inventory, script, screenshot, document, or other dossier evidence. `asset_read`, `asset_list`, `asset_write`, and `asset_delete` use raw-byte SHA-256 `if_match` tokens; text and base64 preserve both UTF-8 and binary content. Asset paths are relative to the expanded owner, cannot be hidden, escape it, or end in `.md`, and are capped at 10 MiB — the largest file still worth versioning in git (D270): `asset_write` refuses a larger one and `asset_read` will not return it. Files also reach an expanded concept through git, so the listing never fails on one it can describe: hidden files and directories (`.DS_Store`, `.gitkeep`) are not assets and are skipped, and a file above the cap is listed with `oversized: true` and can still be deleted.

**Searchable text (D277).** `search` indexes the text of an owner's assets into the owner's own document, so a value that lives only in an inventory CSV, a config or a script finds the owner; the file is then found with `asset_list` and `asset_read`. An asset is indexed only if all hold: its extension is one of `.txt .csv .tsv .json .yaml .yml .toml .ini .conf .cfg .sh .ps1 .py .go .sql .xml .log`; it is not `oversized`; it is valid UTF-8 without NUL bytes, of which the first 256 KiB count (the rest is ignored). Binary formats (PDF, DOCX, images) are not extracted — that needs a dependency; write their content into the concept instead. The indexed text is the assets in path order, each as `<path>\n<text>`, so a path is searchable too. Changes are detected by stat signature (path, size, mtime), not by content, so an asset edited in place on disk is picked up by the next `search`.

An asset is **not** a concept: it has no frontmatter or ConceptID, is never emitted by `WalkConcepts`, validated as OKF, or made a graph node. Its text is searchable through its owner (D277, below); the asset itself is never a search hit. A dossier document can link to it with a relative Markdown file link. Lint reports a junk asset (`__pycache__/…`, `*.pyc`, see `data/.gitignore` above) as `junk_asset` (warning, delete it with `asset_delete`) and never as `orphan_asset`, an uncited asset as `orphan_asset` (info), one above the cap as `oversized_asset` (warning), and an owner whose assets cannot be listed (a symlink or special file inside it) as `unlistable_assets` (warning) rather than aborting the run. Moving an expanded concept moves its assets with it; inbound links from outside that directory to an asset are not rewritten. Deleting one requires explicit `force: true` when assets remain.

## Maps and Journals

Every map/journal declares its kind and the palette of allowed concepts in the `_map.md` descriptor:

```yaml
---
type: Map
title: Smart Home
kind: map                      # map (thematic) | journal (chronological log)
concept_types: [Entity, Topic, Runbook]
ontology_mode: strict          # strict | emergent | off
required_fields: [timestamp]  # optional, required on every concept by lint
required_fields.Runbook: [provenance] # optional, additive for this exact type
field_values.status: [active, draft, deprecated, superseded] # optional, allowed values on every concept (D275)
field_values.Incident.outcome: [open, mitigated, resolved]   # optional, per type; replaces the map-wide list for that field
forbidden_fields: [state]            # optional, fields no concept may carry
require_index_entry: true      # optional, require curated index membership
machine_path_allow_prefixes: [/home/nonroot, /home/ubuntu/.cache/huggingface] # optional, operational path roots (D124)
timestamp: 2026-06-25T10:00:00Z
---
```

A **map** groups by theme, with mixed types (an Entity and a Topic from the same domain coexist: the type is a frontmatter attribute, not a position). A **journal** groups by chronology (dated concepts `YYYY-MM-DD-slug`, append-oriented). `ontology_mode`: `strict` (only `type`s in the palette), `emergent` (new types get registered in a manifest), `off` (no check). `required_fields` is a map-wide lint contract; `required_fields.<Type>` adds fields for an exact, case-sensitive type. `field_values.<field>` restricts a frontmatter field to a list of exact strings (trimmed) on every concept; `field_values.<Type>.<field>` does the same for one type and **replaces** the map-wide list for that field. A list-valued field is valid only if every element is allowed, an absent field is not checked (presence is `required_fields`' job) and an empty allowed list is a malformed key. `forbidden_fields` lists fields no concept may carry (D275). `require_index_entry` requires every map concept in the map `index.md` and every satellite in its expanded owner's `index.md`. `machine_path_allow_prefixes` (D124) lists absolute path prefixes — POSIX or Windows drive-absolute — that the `machine_path` lint treats as this map's operational target paths (e.g. a container image's home directory) rather than client-local paths needing a `{{repo:<key>}}`/`{{path:<nome>}}` placeholder; see §Path portability placeholders in `docs/sync.md`. The server ships no default contract or domain vocabulary.

Read-compat (D77): the legacy `_archive.md` descriptor (`type: Archive`, `archive_type`) remains readable and is treated as a Map with `kind: map`; it is never written again, and lint flags it (`legacy_archive_descriptor`) as a migration backlog item.

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

**Links between concepts** (both syntaxes are seen by the graph, lint, and `concept_move`'s backlink-rewrite, D72 WP0): wiki-links `[[id]]` / `[[id#section]]` with **root-relative** IDs (path from the KB root without `.md`, e.g. `[[smart-home/otbr]]`); markdown links `[text](rel/path.md)` **relative to the file** containing them. The alias form `[[id|text]]` **is** supported (D150) and is what keeps a readable label on the one form that is base-independent; the label is preserved verbatim by `concept_move`. **Fenced code blocks and inline code spans are not scanned** (D150): a Mermaid subroutine node (`N1[["a label"]]`), a POSIX character class (`grep -E "x[[:alpha:]]"`) or a documented example link inside a fence is not a link. `concept_move` does not rewrite one either (D248): it rewrites exactly what the graph extracts. Indented four-space blocks are deliberately still scanned, being indistinguishable from a list continuation. An **extensionless** href that resolves to an existing asset of the citing concept (a `Dockerfile`, a `Makefile`, a `LICENSE`) is treated as an asset citation rather than a ConceptID shorthand, so such an asset can be cited and stops being reported `orphan_asset`.
**One base, and it is the file** (D149). A relative markdown link resolves against the file that contains it, exactly as a markdown viewer resolves it — so from an expanded concept's `index.md` a satellite is `[s](s.md)`, not `[s](c/s.md)`. The graph and lint use that same base; lint's existence check goes through the same resolver `concept_read` uses, so a link to the canonical ID of an expanded concept (`[c](c.md)` where `map/c/index.md` holds it) is valid rather than broken. A link spelled `c/index.md` or `[[map/c/index]]` is the same concept, not a node of its own (D310): the link graph (`buildGraphView`) stores the edge under `map/c`, so backlinks, orphan, `cut_concept`, communities and `link_suggest` count it; `ExtractLinks` and `RewriteLinks` still report the literal ID. Lint's `index_link_form` (info, `kb_repair`-fixable) flags the spelling. A wiki-link `[[id]]` is root-relative and therefore base-independent, which is why it is the safe choice when in doubt.

**In a curated index, both forms are accepted** for an expanded concept: `[c](c.md)` and `[c](c/index.md)` both satisfy `require_index_entry`. The two used to be inverses of each other — one valid between concepts, the other in an index — with nothing to tell them apart.

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
concept's own relative links** (the directory delta is known, so this is arithmetic), and — for maps
that opted in with `require_index_entry` — **both curated indexes**, removing the source entry and
appending one to the destination under a `## Moved here` heading.

Both index edits are conservative. A source line is removed only when the moved concept is its **only**
link: a line citing two concepts is prose the operator wrote, so it is kept and reported instead.
Cartographer does not attempt to place the destination entry in the right thematic section — it cannot
know, and a wrong placement in a curated document is worse than an obvious one at the end.
`rewrite_links: false` still means "touch no other concept", so it skips the index maintenance too.

None of this depends on the namespace: a move from a map into the KB-root `services/`, the reverse,
or a rename within `services/` has the same postconditions as one between two maps — the old ID is not
found, the new one is readable, an expanded concept carries its satellites and asset bytes, and search
follows (D269). Every entry of a batch is validated — IDs, path confinement, source present, destination
free in its real root, duplicates, overlapping expanded moves — before any entry is applied. The
boundary is a filesystem failure after a destination was written (the source cannot be removed, or an
expanded directory cannot be renamed): the call returns an error naming both IDs and what was already
applied, nothing is rolled back, logged or committed, and the operator deletes the extra copy.

A map created without `require_index_entry` opts in later with `map_update` (D229). Until it does, a
move out of it leaves its index entry behind — which `lint` reports as a `broken_link` on the map's
`index.md`, since dead index links are checked for every map.

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
(info, directory-level) when the block differs from what the next write would put there — in place
of `index_incomplete`, which a generated map never gets. A KB's `instructions.md` that tells agents
to add new pages to the index is the `kb-doctor` skill's to update, not the server's.

### Cost keys (D301)

| Key | Meaning | Default |
|---|---|---|
| `index: generated` | the server keeps the map's concept list (§Generated indexes) | `curated` |
| `repeated_fact_min: <n>` | concepts that must carry a line for a `repeated_fact` review item; with owners in several maps the lowest threshold among them applies | 3 |
| `hotspot_in_degree: <n>` | inbound links that, with `hotspot_bytes`, make a `read_hotspot` | 50 |
| `hotspot_bytes: <n>` | body size that, with `hotspot_in_degree`, makes a `read_hotspot` | 16384 |
| `oversize_bytes: <n>` | this map's `concept_oversize` threshold | half the 60 KB read guard |
| `oversize_concepts: <n>` | top-level concepts above which the map is `map_oversize` (D313) | 50 |

Each is a positive integer (or `generated`/`curated`); anything else is `contract_malformed`.

Page-name keys (D315), read by `title_quality`:

| Key | Meaning | Default |
|---|---|---|
| `title_max_length: <n>` | title length (characters) above which a concept of this map is `title_quality`; `0` turns the length rule off | 100 |
| `forbidden_title_terms: [...]` | substrings no title in this map may carry, matched case-insensitively | none |

`title_max_length` is a non-negative integer, `forbidden_title_terms` a non-empty list; anything else is `contract_malformed`. `map_update` sets them (`title_max_length: -1` removes the key, back to the default).

### Accepting a check for a whole map (D306)

`lint_ignore: [check, …]` in a map's `_map.md` accepts the named checks for **every concept of
that map**, and for the findings reported on the map itself: a KB's style choice (a "See also"
that says why each link matters, services that link the infrastructure they run on) is one
decision, not one write per concept. It takes every check a concept can silence (below) plus
`facet_sprawl`, `missing_value_contract`, `map_oversize` and `island`, which no single concept owns; an
`island` also goes when any member, or a member's map, accepts it. Errors never go; a name a map
cannot accept is reported as `lint_ignore_invalid` on the `_map.md`. With this, every finding has
a way out — fixed, or accepted where the KB says so — and a Health panel with no findings is
what a finished `kb-doctor` session leaves.

### Silencing a lint finding on one concept

`lint_ignore: [check, …]` in a concept's frontmatter drops the named findings **for that concept
only** (D159). It exists because a concept documenting a false positive — a `~/.ssh/config` in prose,
a deliberately-broken example link — could not be written without generating the findings it
describes, so a KB's own "known false positives" page was impossible.

Suppressible: `broken_link`, `machine_path`, `concept_oversize`, `stale_claim`, `imported_draft`,
`secrets_on_non_service`, `orphan`, `missing_title`, `unknown_placeholder`, `forbidden_term`, `sops_format_mismatch`, `sops_missing_file`, `legacy_path`, `nonstandard_field`, `prose_value`, `stale_open`, `closed_with_open_items`, `status_semantics` (D321), `template_section_missing`, `open_marker`, `source_uncited`, `mangled_placeholder` (D314), `title_h1_mismatch` and `title_quality` (D315), `duplicate_link`, `bare_link_list`, and the structural
`cut_concept`, `link_to_retired`, `broken_relation`, `map_misfit`, `reciprocal_link_item` (D301), and the `kb_review` kinds (D298, D301) `duplicate_candidate`, `zombie_work`, `harvest_candidate` (D322), `repeated_fact`, `read_hotspot`, `promotion_candidate`, `glossary_gap`, `lint_judgement` — there the name dismisses a review item that names the concept (see §Review keys). **Not** suppressible: `stringified_list` (D314), `tool_param_field` (a tool argument is never a legitimate field), every `error`-severity check
(`missing_required_field`, `invalid_field_value`, `forbidden_field`, `expanded_ambiguous`) — those are contract violations, not judgements, and
letting a concept declare its own contract void would be a hole rather than an escape hatch — and the
directory-level checks (`index_incomplete`, `index_stale`, `expanded_*`, `orphan_asset`,
`oversized_asset`, `unlistable_assets`, `unused_placeholder`, `facet_sprawl`) and the artifact checks
below (`skill_*`, `legacy_tool_name`, `missing_instructions`, `junk_*`, `cross_kb_path`), which belong to a map, an expanded concept or `paths.yaml` and have no single
concept frontmatter that owns them (a map accepts `facet_sprawl` and `map_oversize` in its `_map.md`, above).
`island` is accepted by any of its members. Naming
an unsuppressible or unknown check is itself reported as `lint_ignore_invalid`: a typo that silently
suppresses nothing is worse than no opt-out.

Where each check can be accepted (D313) is `lint.CheckAcceptability` — `concept`, `map` (only the
map's `_map.md`) or `none` — read from the same tables `lint_ignore_invalid` enforces, and shown as
`kb_status.conformance.acceptability` and as a badge on the Health panel.

Structural checks flag defects, not structure (D313): `link_to_retired` is **one finding per retired
concept**, on the retired concept ("retired, still linked by N live concepts: …"; the declared
`superseded_by` successor and linkers in a journal are not counted), so retiring a component is one
decision and `lint_ignore: [link_to_retired]` on it accepts the remaining mentions as historical;
`cut_concept` does not count an expanded concept's own satellites or a journal's entries among the
nodes a vertex separates; `secrets_on_non_service` is `info` and points at `secret_resolve`; and
`machine_path` never fires for the conventional tool paths `~/.ssh/`, `~/.kube/`, `~/.m2/`,
`~/.config/`, `~/.cache/`, `~/.local/`, `~/.gnupg/` (a map's `machine_path_allow_prefixes` only adds
to them). `missing_value_contract` skips a field whose every value is a date.

### Conformance checks (D289)

A write response carries these checks for the concept it wrote, plus the body, link and light graph checks `lint.ScopedCheck` runs without walking the KB (D312, `control-plane.md` §`concept_write`); the whole-KB structural checks stay with `lint` and `gate_check`.

Lint also compares a KB with the standard fields the server reads, not only with the contracts the KB declared. All are warning or info, so `gate_check` never fails on them, and a check fires only on a field the KB actually has.

- `nonstandard_field` (warning): a frontmatter key that is a known synonym of a standard field. Standard field absent: the finding carries the fix `rename_field <synonym> → <standard>`. Standard field also present: the finding says so and has no fix, since merging values is a judgement. A synonym a map names in `required_fields` is still reported, with a note that the contract names it too. Keys match case-insensitively. The synonym table (`StandardFieldSynonyms` in `internal/lint/conformance.go`):

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

- `tool_param_field` (warning, not suppressible): a key named like a parameter of a concept-write tool (`lint.ToolParamFields`: `id`, `frontmatter`, `body`, `if_match`, `template`, `vars`, `old_string`, `new_string`, `replace_all`, `edits`, `unset`, `operations`, `op`), fix `drop_field`. The same write is rejected, see `docs/control-plane.md`.
- `missing_value_contract` (info, on the map's `_map.md`): a scalar string field present in at least 5 concepts of the map, with at most 8 distinct values (no cap for `status`, the vocabulary every reader assumes) and no `field_values` for it (map-wide or for the dominant type). The message lists the observed values with their counts and the line to add, typed (`field_values.<Type>.<field>`) when at least 90 % of the carrying concepts share one type. `title`, `type`, `description`, `timestamp`, `review_after`, `superseded_by`, the free-form or list-shaped `provenance`, `tags`, `resource`, `secrets_source`, and the synonyms above are never suggested (D295). No fix: declaring a vocabulary is a judgement.

- `malformed_frontmatter` (warning, not suppressible, no fix, D295): a top-level key with a scalar value followed by indented `- ` lines. The stdlib-only parser (D8) keeps the scalar and drops the lines, so the value is silently truncated; the message names the key and the line.
- `stringified_list` (warning, not suppressible, fix `listify_field`, D314): a list field (`provenance`, `tags`, `related`, `lint_ignore`, `open`, `secrets_source`) whose parsed value is a string that looks like a list: `"[a, b]"`, `"[a]; [b]"` or `"- a"`. The parser reads a quoted flow list as a scalar, so nothing else sees it. `kb_repair` rewrites it as a real list.
- `title_h1_mismatch` (warning, suppressible, fix `sync_h1`, D315): the frontmatter `title` and the body's first `# ` heading both exist and differ. The title is the label `concept_list`, search and the Atlas show, so the heading is the wrong one: `kb_repair title_h1_mismatch` overwrites it with `# <title>` (plain text: formatting in the old heading goes; only the first heading, never one in a code fence).
- `title_quality` (info, suppressible, no fix, D315): the title carries a decorative character (Unicode symbol or modifier, emoji included), is longer than the map's `title_max_length`, holds a lifecycle word (`attivo`, `active`, `dismesso`, `deprecated`, `draft`, `superseded`, `preparazione`, `archiviato`, `archived`, `declassato`, whole words) while the concept has a `status` field, holds one of the map's `forbidden_title_terms`, or the concept's own slug starts `YYYY-MM` in a map that is not `kind: journal`. One finding per rule that fires; a KB that wants it stricter promotes nothing here, it fixes the titles.
- `mangled_placeholder` (warning, suppressible, no fix, D314): the body holds a `` `repo:key` `` or `` `path:key` `` code span followed within 40 characters by "between double braces" (or its Italian/French forms): a `{{…}}` placeholder an import unwrapped into prose. Fenced blocks and lines containing `{{` are skipped.

Two finding kinds gained a mechanical fix in D295. `broken_link` in the `index.md` of an expanded concept carries `rebase_link` (`field` the href, `to` the rewritten href) when the link resolves against the pre-expansion file `<id>.md`: that is the damage an expansion did before `concept_expand` rebased links. `duplicate_link` is one finding per repeated target, and carries `drop_link_item` (`field` the exact list line) when that item is a single link and nothing else: the link stays in the text, the item goes, and a section left with no item loses its heading. An item with any other word keeps no fix — the word may be the reason. A `rebase_link` with an empty `to` (D310) is a link that resolves to the concept itself: the repair keeps the label and drops the link syntax. `index_link_form` (D310, info) flags a link spelled `<concept>/index` where the concept exists: a markdown href gets `rebase_link` to the relative `<concept>.md`, a wiki-link `rewrite_wiki_link` (`field` the old ID, `to` the new; alias and anchor are kept). `reciprocal_link_item` (D301, info) carries the same fix for a link-only item whose target links back to the concept from its text — a back-link only in the target's own links section does not count, so a mutual pair listed only in the two links sections is never flagged and repairing it can never drop the edge (D309): backlinks keep the edge navigable both ways, so the item is a second write for an edge the server already exposes. It is an efficiency choice the operator opts into (`kb_repair reciprocal_link_item`, or `auto_repair` when listed), not conformance debt, and refines D287 without reverting it: only reciprocated items go, the section stays. `map_misfit` names only a map whose contract admits the concept's type (a strict map's `concept_types`); with no admitting majority there is no finding.

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

- `missing_value_contract` on `status` folds each family onto its majority member and each prose value onto its leading token, and carries a structured `proposal: {key, field, type?, values, mapping}` next to the message. Declaring it is the operator's judgement (`map_update`); after that the per-concept findings are mechanical.
- `invalid_field_value` carries `set_value` when the value is, up to folding or by family, exactly one allowed value. Two candidates, or none, give no fix.
- `prose_value` (warning, suppressible): `status`, or a field the contract constrains, holds a sentence (a separator such as ` — `, `;`, `: `, or more than three words). It carries `split_value` when the leading token is a contract value or, with no contract, a family member: the field keeps the token and `kb_repair` writes the rest as `> <field>: <rest>` after the first heading, so nothing is lost.

### Decay checks (D297)

Lint also sees a KB **decaying**: work never closed, closed work not finished, pages without the shape their template promises, open questions nobody counts, facets that stopped being facets. All are `info`, never a gate, and judgement (no fix): they feed the doctor. A map contract tunes them with four keys, also settable through `map_update`:

| Key | Meaning | Default |
|---|---|---|
| `open_statuses: [...]` | statuses that mean "not finished" | the `in-progress`, `blocked`, `proposed`, `draft` families, `open`, `decision-needed`; never the `active` family, which means "the page is valid" in a journal and in a map alike (D321). `map_create` with `kind: journal` writes `open_statuses: [open, in-progress, blocked]`; a KB that reads `active` as open lists it |
| `stale_after: <days>` | age after which an open concept is stale | 60 in a journal; none in a map (a reference page is not stale by age) |
| `harvest_after: <days>` | age after which a closed journal entry is a `harvest_candidate` (D322) | 45 |
| `template_sections: true` | pages must carry the H2 sections of `templates/<type>.md` | off: many KBs use templates as guidance, not a schema |
| `open_markers: [...]` | words that mark an open question, in the KB's language | `TODO`, `TBD`, `FIXME` |

- `stale_open` (suppressible): open status and `timestamp` older than `stale_after`, unless the concept declares a `review_after` today or later: that suspends the timer (D321); a past `review_after` does not, it makes the wait overdue (and `stale_claim` fires). `waiting_on` is a free-text field naming who or what blocks the work, with no vocabulary; the doctor sets both on a finding only the operator can resolve.
- `status_semantics` (warning, suppressible, D321): `status` in the `active` family on a concept of a journal whose `open_statuses` does not list it, which is checked on write too. The review kind `status_reclassify` proposes the replacement.
- `closed_with_open_items` (suppressible): a status of the `done` or `resolved` family with unchecked `- [ ]` items outside code and outside a section whose H2 matches the map's `procedure_headings` (default `procedure`, `steps`, `how to`: a procedure's checklist is a reusable template, D313).
- `template_section_missing` (suppressible): sections of the type's template the page lacks, compared folding case and accents; headings inside fenced code in the template are ignored.
- `open_marker` (suppressible): marker occurrences outside code, outside heading lines, outside table rows of a concept in an open phase (D313), and outside struck-through `~~text~~` (closed or cancelled, D307), whole words, case- and accent-folded; `kb_status.open_markers` totals them as `{concepts, markers}`.
- `facet_sprawl` (on `_map.md`, directory-level): `tags` with at least 30 distinct values, half or more used once; the message lists the ten most used as the likely vocabulary.

### The `archived` status (D322)

`archived` is a server-reserved status, after `done`: the entry is finished **and** its durable facts have been carried into the live pages it links. It is retired like `deprecated` and `superseded` (`link_to_retired` counts live linkers, `map_misfit` and `link_suggest` skip it; a `digest`-typed page linking archived entries is not a linker) and closed, never open, and in no synonym family: it is a lifecycle stage, not a value a KB chooses among synonyms. An archived concept stays readable and linkable but leaves the active surface: default `search` (with an archived fallback, `docs/control-plane.md`), `read_cost`, the `atlas_overview` structure graph and the `map_oversize` count. The other graph tools still show it; `deprecated` and `superseded` concepts still count in `read_cost`. The bundled `kb-doctor` skill does the harvest: facts into live pages with a dated link back, `status: archived` with a header line, one `reference` digest per quarter. Nothing is deleted or moved.

### Review keys (D298)

`kb_review` (`docs/control-plane.md`) builds the doctor's work list from the graph, the lint findings and the search index. Three contract keys, also settable through `map_update`, tell it where the KB keeps what the server must not guess:

| Key | Meaning | Default |
|---|---|---|
| `promote_to: <map>` | the map a journal's reusable procedures belong in: a concept with 5+ consecutive numbered items, or an H2 matching `procedure_headings`, and no link into it is a `promotion_candidate` | none: no promotion candidates |
| `procedure_headings: [...]` | H2 prefixes that mark a procedure, in the KB's language, matched case- and accent-folded | `procedure`, `steps`, `how to` |
| `glossary: true` | this map is where the KB defines its terms: a term used in any of its concepts is never a `glossary_gap` | off: only `glossary.yaml` defines terms |
| `work_map: <map>` | the existing map where this map's work belongs (D302): a concept here with unchecked items or an open-phase status and no link into it is `scattered_work`; a name that is no map is `contract_malformed` | none: work may live anywhere |

**Work items (D302).** A work item is a concept in an open phase for its map (`open_statuses`, else the defaults; never `active`, D321; the `draft` family only where `open_statuses` lists it, since it marks a page still being written), of any type, or an unchecked `- [ ]` item outside code in any concept, whatever its status. `work_list` returns them read-only; staleness is `stale_open`'s threshold, extended to a concept whose unchecked items are that old.

A review item is dismissed by `lint_ignore: [<kind>]` on a concept it names — for a pair, either member; a `glossary_gap` instead stops counting the concept carrying it, and the item goes when fewer than 10 remain. The agent writes the dismissal with the reason in the same commit, so the history says why; there is no review state besides the KB itself.

A finding whose remedy is mechanical carries `fix: {kind, field, to?}` (`rename_field`, `drop_field`, `rebase_link`, `drop_link_item`, `rewrite_wiki_link`, `set_value`, `split_value`, `sync_h1`); the rest carry none. `lint.CheckConcept` computes the frontmatter-driven checks of one concept without walking the KB (`missing_required_field`, `invalid_field_value`, `forbidden_field`, `nonstandard_field`, `tool_param_field`, `missing_title`, `title_h1_mismatch`, `title_quality`, `machine_path`, `stale_claim`); `Run` calls the same function, and the write tools return its result.

`machine_path_allow_prefixes` accepts **`~/`-anchored** prefixes as well as POSIX- and
Windows-absolute ones: `~/.ssh/config` means "your ssh config" on every machine, exactly as `/etc/…`
does. Matching is literal and at segment boundaries — `~/.ssh` covers `~/.ssh/config` but not
`~/.sshx` — with no home expansion anywhere, so the contract's meaning does not depend on the
reader's home directory. `~user/…` is rejected, since the detector never produces that form.

The two "too big" numbers are now related: `concept_oversize` fires at **half** the size at which
`concept_read` degrades to an outline, so an author gets warning before reads change shape, and the
message names both bounds. For a satellite the message does not advise `concept_expand` — the write
path caps depth at three segments, so that remedy is structurally unavailable — and names splitting
into sibling satellites instead.

### Artifact checks (D316)

Lint also covers what a KB ships beside its concepts. All are warning or info; the KB-level ones run
only in a whole-KB lint, and their findings name the artifact file (`skills/<name>/SKILL.md`,
`agents/<name>.md`, `instructions.md`), never a concept.

- `skill_invalid` (warning): `skill.Validate` would refuse the skill (unreadable frontmatter, name
  rules, name ≠ directory, no description), or a `skills/<dir>` has no `SKILL.md`.
- `skill_warning` (info): `skill.Validate`'s warnings (description over 1024 characters, body over
  500 lines).
- `legacy_tool_name` (warning, fix `strip_tool_prefix`): a pre-D288 prefixed tool name
  (`<kb>__search`, e.g. kb_a__search) in a skill, agent or `instructions.md` (Markdown, text, scripts and config
  files; stylesheets are skipped, a BEM class has the same shape). A client's own MCP tool name,
  `mcp__<server>__<tool>` (Claude Code, Codex), is never flagged: it is current, not a prefix. The message names the mounted KB
  the prefix most likely meant, so the agent adds `kb: "<name>"`; the fix only strips the prefix.
- `skill_broken_ref` (warning): a `tools/`, `scripts/` or `skills/` path in a skill's code (fenced
  block or inline code — prose is not scanned) that exists neither under the KB root nor in the
  skill's directory. The MCP methods `tools/list` and `tools/call` are protocol, not paths.
- `skill_git_command` (info): a skill's code runs `git add|commit|push|pull|merge|rebase|reset|checkout|stash`;
  the KB is written only through MCP tools. `git clone`, `status`, `log` are reads.
- `missing_instructions` (warning, KB-level, no path): more than 10 concepts and no `instructions.md`.
- `junk_file` (warning): a junk file git tracks, or would add at the next commit (`git ls-files
  --cached --others --exclude-standard`), outside an expanded concept's assets; remove it with `git
  rm`. `junk_asset` covers the assets (§Assets).
- `sops_format_mismatch` (warning, also per concept, suppressible): a `sops decrypt` (or `-d`)
  piped to `jq` or `python3 … json.load` without `--output-type json` in code — sops prints YAML by
  default. `sops_missing_file` (warning, same scope): with a `secrets/` directory in the KB, a
  `sops decrypt secrets/…` naming a file that does not exist. Nothing is decrypted or read.
- `cross_kb_path` (warning): an artifact hard-codes a sibling KB's root (or
  `~/cartographer-data/<name>`). The server injects the siblings' roots when it mounts more than
  one KB (`kb.KB.SiblingRoots`); a single-KB server has none and pays nothing.
- `skill_missing_perimeter` (info): `instructions.md` declares `perimeter` and a skill's
  description does not contain it (case-insensitive substring, so `ops` matches `DevOps`).
- `artifact_unused` (info): a skill or agent no client has activated for `usage_stale_days`
  (default 42, per KB in the server config, `0` disables; D326). Three messages: *never
  activated* (absent from the usage reports), *never seen activated, catalogue loaded N days
  ago* (the only signal is a Codex catalogue load, which proves availability, not use), and
  *last activated N days ago*. With no usage report at all the check is silent — the absence
  of a signal is not evidence of disuse. The usage is local state, below.
  An artifact added (or renamed into place) inside the threshold, by the KB's git history, is
  never reported as *never activated*: it has not had the time to be used (D336).
- `legacy_path` (warning, per concept, suppressible, fix `replace_prefix`): a concept body contains
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
`artifact_unused`, the `usage` section of `kb_status` and the Atlas Artifacts panel's "Last used"
column. Written only by `POST /api/usage` (`docs/control-plane.md` §Usage reports).

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
optional on repo keys only, a clone URL or `host/owner/name`, stored normalized. It is written with
git or `artifact_write` (validated strictly: any malformed entry refuses the write, naming it) and
read tolerantly (a malformed entry is left out). It is not a concept and is never materialized on a
client: `sync_pull` serves it as data, and the client uses it to resolve keys
(`docs/sync.md` §The KB's placeholder registry).

When the file exists, `lint` adds three checks — none when it does not, so an existing KB is not
flooded on upgrade:

- `unknown_placeholder` (warning, per concept, suppressible): the concept cites a key not declared
  under the matching kind. One finding per concept, naming every undeclared key; the keys come from
  the graph cache's placeholder facet.
- `missing_registry` (info, KB level, D314): placeholders are cited but `paths.yaml` does not exist. One finding on `paths.yaml` with the count; creating the file dismisses it (a KB root has no frontmatter for `lint_ignore`). A `paths.yaml` that exists but is unreadable gives `contract_malformed`, not this.
- `unused_placeholder` (info, on `paths.yaml`, whole-KB lint only): a declared key no concept and no
  artifact (`skills/`, `agents/`, `hooks/`, `mcp/`, `instructions.md`) cites.
- `hook_invalid` (warning, on `hooks/<name>/hook.json`, whole-KB lint only): a hook whose `hook.json` `artifact_write` would now refuse (invalid JSON, missing `event`/`command`, a command outside the hook's directory) or warn about (an event outside the declared vocabulary, which never fires if misspelled), so hooks written before that validation surface. Independent of `paths.yaml`: it applies to every KB with a `hooks/` directory ([D284](decisions/D284-hook-json-is-validated-against-one-declared-event-vocabulary.md)).
- `contract_malformed` (info, on `paths.yaml`): a malformed entry, or a file that is not a YAML
  mapping at all — in which case no key is reported undeclared.

`machine_path` also names the declared key whose `default` is a prefix of the flagged path, e.g.
`~/.claude/settings.json` → "use `{{path:claude-home}}/settings.json`". It stays one finding per
concept but names every other disallowed path in it ("and 2 more here: …"), without the prose
punctuation that follows a path, so fixing the first is not how the author learns of the next.

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
- **Lint.** `forbidden_term` (warning, per concept, suppressible with `lint_ignore` — a migration
  note may quote an old name): the body uses a forbidden term as a whole word outside fenced and
  inline code (the masking the link graph uses, D150). One finding per distinct term, naming the
  canonical. A malformed entry is `contract_malformed` (info, on `glossary.yaml`, whole-KB lint only).

## Extended concept types

`Service`, `Contradiction` and `Source` are conventional types used by dedicated tools.
`validate()` enforces the normal frontmatter/layout rules and, in a strict Map,
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
  `ingested` Source nobody cites as `source_uncited` (warning, suppressible);
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
