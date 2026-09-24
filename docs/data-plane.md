# Data Plane — the Knowledge Base model

The data plane is the **source of truth**: UTF-8 `.md` files with YAML frontmatter, organized in a fixed hierarchy, versioned in git. The Go server holds no critical state: everything can be rebuilt from the files.

## Hierarchy

| Level | Name | What it is | OKF mapping |
|---|---|---|---|
| 1 | **Atlas** | A self-contained knowledge base; an instance hosts one or more | OKF bundle = git repository |
| 2 | **Map** / **Journal** | Map: a thematic domain with mixed `concept_types` (e.g. `smart-home`, `infra`). Journal: a chronological, append-oriented log (e.g. `incidents`, `notes`) | Top-level subdirectory, described by `_map.md` (`kind: map\|journal`) |
| 3 | **Concept** | A single knowledge page | `.md` file with frontmatter |

There is no intermediate categorization level (D77): category navigation is the job of curated `index.md` files, `search`, and the graph — not the filesystem. A growing concept becomes an **expanded concept** — a *state*, not a level: `concept_expand` turns `map/name.md` into `map/name/index.md` **without changing the ConceptID** (ID resolution tries `<id>.md` and then `<id>/index.md`, so no backlink breaks), and from there the concept can grow with `map/name/child` satellites and assets. Expansion is the prerequisite for owning assets. Expansion is also allowed in journals (e.g. a heavy incident with attachments). `concept_collapse` is its inverse (D160): `map/name/index.md` becomes `map/name.md` under the same ConceptID, so no inbound link changes. It refuses while the directory still holds satellites, assets or anything else besides `index.md` (a hidden file) — none would have a home after the collapse — and names them. `concept_merge` folds a satellite into its own parent, rebasing the merged body's relative links and redirecting every inbound link, including those from sibling satellites; both are `advanced`, i.e. callable by name but not advertised in `tools/list`.

Depth is **enforced on the write path** (D72 WP4): a ConceptID under `data/` has at most 3 segments (`map/concept/child`, where the third segment only exists inside an expanded concept); deeper writes are rejected. Reads are unaffected (legacy KBs remain readable). If a write implicitly creates a new expansion directory (e.g. `concept_move` into a nested path), the server also generates the `index.md` stub (`type: Index`, title from the name) — so `index_get`'s progressive disclosure never breaks. Lint defends the semantics of the hierarchy (D77 WP4, `concept_oversize` D78): `expanded_missing_index` (a directory with no `index.md`), `expanded_ambiguous` (both `<id>.md` and `<id>/index.md` exist: writes are blocked until one form is removed), `expanded_as_category` (many children not linked from the concept's index: the directory is being used as a taxonomy), `map_oversize` (a map beyond the size threshold: a thematic split is preferable to a subfolder), `legacy_archive_descriptor` (a pre-D77 `_archive.md` descriptor), `concept_oversize` (a concept beyond the byte threshold: a candidate for `concept_expand` into a dossier).

Every KB (Atlas) is split into two planes: the **conceptual root** (`data/`), which holds maps, journals, and concepts; and the support folders (`skills/`, `services/`, `agents/`, `hooks/`, `templates/`), which sit directly under the KB root. KBs are **isolated**: no cross-links between different KBs.

## Filesystem layout of a KB

```
kb-<domain>/                          # git repo = OKF bundle (content directories only, D62)
├── .sops.yaml                         # creation_rules for encrypted secrets
├── .gitattributes                     # diff=sopsdiffer for *.sops.yaml
│
├── data/                              # CONCEPTUAL ROOT
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
└── paths.yaml                         # optional: declared {{path:…}}/{{repo:…}} keys (D263)
```

`services/` is included in `WalkConcepts` (search, graph, lint all see it) but its root is `kb.Root`, not `kb.DataRoot()`. Service concept IDs carry the `services/` prefix. `ResolvePath` is the one place that picks the root for an ID, and every operation that turns an ID into a file — read, write, collision check, removal, `concept_move` — goes through it (`LocateConcept` for callers that move files themselves). For the same reason `services` is not a valid map or journal name: `map_create` refuses it, since the scaffold would land under `data/services/`, where no read looks (D269). A `data/services/` left by an older version is neither read nor cleaned up. `agents/` and `hooks/` are not concepts (no OKF frontmatter, they don't go through `WalkConcepts`): they are provisioning artifacts materialized client-side — see `docs/sync.md` §Agents and hooks.

`templates/` is outside `WalkConcepts`: templates have no ConceptID and are never indexed, linted or added to the graph. A template is a KB-only artifact, not a provisioning kind: it is maintained through `artifact_*`, discovered with `template_list`, and used once by `concept_new`; it never affects a provisioning manifest or its revision.

`paths.yaml` is likewise KB-only data, not a concept and not a provisioning artifact: the KB's
declared placeholder vocabulary, maintained through `artifact_*` or git and served to clients by
`sync_pull` (§The path placeholder registry below).

### Assets

An **asset** is a regular, non-Markdown file inside an expanded concept directory: a CSV inventory, script, screenshot, document, or other dossier evidence. `asset_read`, `asset_list`, `asset_write`, and `asset_delete` use raw-byte SHA-256 `if_match` tokens; text and base64 preserve both UTF-8 and binary content. Asset paths are relative to the expanded owner, cannot be hidden, escape it, or end in `.md`, and are capped at 10 MiB — the largest file still worth versioning in git (D270): `asset_write` refuses a larger one and `asset_read` will not return it. Files also reach an expanded concept through git, so the listing never fails on one it can describe: hidden files and directories (`.DS_Store`, `.gitkeep`) are not assets and are skipped, and a file above the cap is listed with `oversized: true` and can still be deleted.

An asset is **not** a concept: it has no frontmatter or ConceptID, is never emitted by `WalkConcepts`, indexed by search, validated as OKF, or made a graph node. A dossier document can link to it with a relative Markdown file link. Lint reports an uncited asset as `orphan_asset` (info), one above the cap as `oversized_asset` (warning), and an owner whose assets cannot be listed (a symlink or special file inside it) as `unlistable_assets` (warning) rather than aborting the run. Moving an expanded concept moves its assets with it; inbound links from outside that directory to an asset are not rewritten. Deleting one requires explicit `force: true` when assets remain.

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
require_index_entry: true      # optional, require curated index membership
machine_path_allow_prefixes: [/home/nonroot, /home/ubuntu/.cache/huggingface] # optional, operational path roots (D124)
timestamp: 2026-06-25T10:00:00Z
---
```

A **map** groups by theme, with mixed types (an Entity and a Topic from the same domain coexist: the type is a frontmatter attribute, not a position). A **journal** groups by chronology (dated concepts `YYYY-MM-DD-slug`, append-oriented). `ontology_mode`: `strict` (only `type`s in the palette), `emergent` (new types get registered in a manifest), `off` (no check). `required_fields` is a map-wide lint contract; `required_fields.<Type>` adds fields for an exact, case-sensitive type. `require_index_entry` requires every map concept in the map `index.md` and every satellite in its expanded owner's `index.md`. `machine_path_allow_prefixes` (D124) lists absolute path prefixes — POSIX or Windows drive-absolute — that the `machine_path` lint treats as this map's operational target paths (e.g. a container image's home directory) rather than client-local paths needing a `{{repo:<key>}}`/`{{path:<nome>}}` placeholder; see §Path portability placeholders in `docs/sync.md`. The server ships no default contract or domain vocabulary.

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
status: active                       # draft | active | superseded | disputed | deprecated
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

The graph retrieval tools (`graph_context`, `link_suggest`, `graph_path`, D242) run on the subgraph induced by the concepts the caller may see: hidden concepts are removed before anything is computed, so a narrowed token gets the answer the same tool gives on a KB in which those concepts do not exist, with no whole-KB restriction needed. `graph_context` and `link_suggest` treat a link as evidence of relatedness in both directions (the undirected projection); `graph_path` can also follow links only as written. A missing concept and a hidden one produce the same `not found` error.

## Naming and concept IDs

Concept ID = path relative to the bundle without `.md`. File names are `kebab-case`. In journals, concepts are dated (`YYYY-MM-DD-slug`); in maps they have durable thematic names. A ConceptID never changes with expansion: `map/name` resolves to `map/name.md` or, once expanded, to `map/name/index.md`.

**Links between concepts** (both syntaxes are seen by the graph, lint, and `concept_move`'s backlink-rewrite, D72 WP0): wiki-links `[[id]]` / `[[id#section]]` with **root-relative** IDs (path from the KB root without `.md`, e.g. `[[smart-home/otbr]]`); markdown links `[text](rel/path.md)` **relative to the file** containing them. The alias form `[[id|text]]` **is** supported (D150) and is what keeps a readable label on the one form that is base-independent; the label is preserved verbatim by `concept_move`. **Fenced code blocks and inline code spans are not scanned** (D150): a Mermaid subroutine node (`N1[["a label"]]`), a POSIX character class (`grep -E "x[[:alpha:]]"`) or a documented example link inside a fence is not a link. `concept_move` does not rewrite one either (D248): it rewrites exactly what the graph extracts. Indented four-space blocks are deliberately still scanned, being indistinguishable from a list continuation. An **extensionless** href that resolves to an existing asset of the citing concept (a `Dockerfile`, a `Makefile`, a `LICENSE`) is treated as an asset citation rather than a ConceptID shorthand, so such an asset can be cited and stops being reported `orphan_asset`.
**One base, and it is the file** (D149). A relative markdown link resolves against the file that contains it, exactly as a markdown viewer resolves it — so from an expanded concept's `index.md` a satellite is `[s](s.md)`, not `[s](c/s.md)`. The graph and lint use that same base; lint's existence check goes through the same resolver `concept_read` uses, so a link to the canonical ID of an expanded concept (`[c](c.md)` where `map/c/index.md` holds it) is valid rather than broken. A wiki-link `[[id]]` is root-relative and therefore base-independent, which is why it is the safe choice when in doubt.

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

### Silencing a lint finding on one concept

`lint_ignore: [check, …]` in a concept's frontmatter drops the named findings **for that concept
only** (D159). It exists because a concept documenting a false positive — a `~/.ssh/config` in prose,
a deliberately-broken example link — could not be written without generating the findings it
describes, so a KB's own "known false positives" page was impossible.

Suppressible: `broken_link`, `machine_path`, `concept_oversize`, `stale_claim`, `imported_draft`,
`secrets_on_non_service`, `orphan`, `missing_title`, `unknown_placeholder`, and the structural
`cut_concept`, `link_to_retired`, `broken_relation`, `map_misfit`. **Not** suppressible: every `error`-severity check
(`missing_required_field`, `expanded_ambiguous`) — those are contract violations, not judgements, and
letting a concept declare its own contract void would be a hole rather than an escape hatch — and the
directory-level checks (`map_oversize`, `index_incomplete`, `expanded_*`, `orphan_asset`,
`oversized_asset`, `unlistable_assets`, `unused_placeholder`), which belong to a map, an expanded concept or `paths.yaml` and have no single
concept frontmatter that owns them, and
`island`, which belongs to a whole component of the graph. Naming
an unsuppressible or unknown check is itself reported as `lint_ignore_invalid`: a typo that silently
suppresses nothing is worse than no opt-out.

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
- `unused_placeholder` (info, on `paths.yaml`, whole-KB lint only): a declared key no concept and no
  artifact (`skills/`, `agents/`, `hooks/`, `mcp/`, `instructions.md`) cites.
- `contract_malformed` (info, on `paths.yaml`): a malformed entry, or a file that is not a YAML
  mapping at all — in which case no key is reported undeclared.

`machine_path` also names the declared key whose `default` is a prefix of the flagged path, e.g.
`~/.claude/settings.json` → "use `{{path:claude-home}}/settings.json`".

## Extended concept types

`Service` and `Contradiction` are conventional types used by dedicated tools.
`validate()` enforces the normal frontmatter/layout rules and, in a strict Map,
that the type is present in the Map's `concept_types` palette. It does not
validate a nested per-type grammar.

- A `Service` commonly carries flat fields such as `kind`, `base_url` and
  `secrets_source` or `secret_refs`; secrets may be owned by any concept, not
  only a Service. See [skills, services and secrets](skills-services-secrets.md).
- The contradiction tools use `resolution_status`, `contradiction_kind`,
  `involves` and `reason`.

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
