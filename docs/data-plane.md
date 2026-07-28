# Data Plane — the Knowledge Base model

The data plane is the **source of truth**: UTF-8 `.md` files with YAML frontmatter, organized in a fixed hierarchy, versioned in git. The Go server holds no critical state: everything can be rebuilt from the files.

## Hierarchy

| Level | Name | What it is | OKF mapping |
|---|---|---|---|
| 1 | **Atlas** | A self-contained knowledge base; an instance hosts one or more | OKF bundle = git repository |
| 2 | **Map** / **Journal** | Map: a thematic domain with mixed `concept_types` (e.g. `smart-home`, `infra`). Journal: a chronological, append-oriented log (e.g. `incidents`, `notes`) | Top-level subdirectory, described by `_map.md` (`kind: map\|journal`) |
| 3 | **Concept** | A single knowledge page | `.md` file with frontmatter |

There is no intermediate categorization level (D77): category navigation is the job of curated `index.md` files, `search`, and the graph — not the filesystem. A growing concept becomes an **expanded concept** — a *state*, not a level: `concept_expand` turns `map/name.md` into `map/name/index.md` **without changing the ConceptID** (ID resolution tries `<id>.md` and then `<id>/index.md`, so no backlink breaks), and from there the concept can grow with `map/name/child` satellites and assets. Expansion is the prerequisite for owning assets. Expansion is also allowed in journals (e.g. a heavy incident with attachments). There is no inverse operation (`concept_collapse`, YAGNI — D77).

Depth is **enforced on the write path** (D72 WP4): a ConceptID under `data/` has at most 3 segments (`map/concept/child`, where the third segment only exists inside an expanded concept); deeper writes are rejected. Reads are unaffected (legacy KBs remain readable). If a write implicitly creates a new expansion directory (e.g. `concept_move` into a nested path), the server also generates the `index.md` stub (`type: Index`, title from the name) — so `index_get`'s progressive disclosure never breaks. Lint defends the semantics of the hierarchy (D77 WP4, `concept_oversize` D78): `expanded_missing_index` (a directory with no `index.md`), `expanded_ambiguous` (both `<id>.md` and `<id>/index.md` exist: writes are blocked until one form is removed), `expanded_as_category` (many children not linked from the concept's index: the directory is being used as a taxonomy), `map_oversize` (a map beyond the size threshold: a thematic split is preferable to a subfolder), `legacy_archive_descriptor` (a pre-D77 `_archive.md` descriptor), `concept_oversize` (a concept beyond the byte threshold: a candidate for `concept_expand` into a dossier).

Every KB (Atlas) is split into two planes: the **conceptual root** (`data/`), which holds maps, journals, and concepts; and the support folders (`skills/`, `services/`, `agents/`, `hooks/`), which sit directly under the KB root. KBs are **isolated**: no cross-links between different KBs.

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
└── hooks/                             # HOOKS (provisioning kind: hook, D48)
    └── <name>/
        ├── hook.json                  # descriptor: event, matcher, command
        └── <script>                   # executable invoked by the hook
```

`services/` is included in `WalkConcepts` (search, graph, lint all see it) but its root is `kb.Root`, not `kb.DataRoot()`. Service concept IDs carry the `services/` prefix. `agents/` and `hooks/` are not concepts (no OKF frontmatter, they don't go through `WalkConcepts`): they are provisioning artifacts materialized client-side — see `docs/sync.md` §Agents and hooks.

### Assets

An **asset** is a regular, non-Markdown file inside an expanded concept directory: a CSV inventory, script, screenshot, document, or other dossier evidence. `asset_read`, `asset_list`, `asset_write`, and `asset_delete` use raw-byte SHA-256 `if_match` tokens; text and base64 preserve both UTF-8 and binary content. Asset paths are relative to the expanded owner, cannot be hidden, escape it, or end in `.md`, and are capped at 1 MiB.

An asset is **not** a concept: it has no frontmatter or ConceptID, is never emitted by `WalkConcepts`, indexed by search, validated as OKF, or made a graph node. A dossier document can link to it with a relative Markdown file link. Lint reports an uncited asset as `orphan_asset` (info). Moving an expanded concept moves its assets with it; inbound links from outside that directory to an asset are not rewritten. Deleting one requires explicit `force: true` when assets remain.

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
timestamp: 2026-06-25T10:00:00Z
---
```

A **map** groups by theme, with mixed types (an Entity and a Topic from the same domain coexist: the type is a frontmatter attribute, not a position). A **journal** groups by chronology (dated concepts `YYYY-MM-DD-slug`, append-oriented). `ontology_mode`: `strict` (only `type`s in the palette), `emergent` (new types get registered in a manifest), `off` (no check). `required_fields` is a map-wide lint contract; `required_fields.<Type>` adds fields for an exact, case-sensitive type. `require_index_entry` requires every map concept in the map `index.md` and every satellite in its expanded owner's `index.md`. The server ships no default contract or domain vocabulary.

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

**Bundle-relative** links starting with `/` (stable, path from the KB root). A link A→B asserts a relationship (the prose supplies the type). Broken links are legitimate stubs. The emergent graph is what lint walks for scoping.

## Naming and concept IDs

Concept ID = path relative to the bundle without `.md`. File names are `kebab-case`. In journals, concepts are dated (`YYYY-MM-DD-slug`); in maps they have durable thematic names. A ConceptID never changes with expansion: `map/name` resolves to `map/name.md` or, once expanded, to `map/name/index.md`.

**Links between concepts** (both syntaxes are seen by the graph, lint, and `concept_move`'s backlink-rewrite, D72 WP0): wiki-links `[[id]]` / `[[id#section]]` with **root-relative** IDs (path from the KB root without `.md`, e.g. `[[smart-home/otbr]]`); markdown links `[text](rel/path.md)` **relative to the file** containing them. The alias form `[[id|text]]` is not supported.

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
