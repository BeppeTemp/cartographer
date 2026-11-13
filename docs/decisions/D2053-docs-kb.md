---
topic: client-configurator
---

# D2053 — The documentation is a generated knowledge base: small units, a hub per page, decisions as leaves

**Decision.** `internal/docskb` turns the repository's own markdown (`README.md`, `CONTRIBUTING.md`, `docs/*.md`, `docs/brand/*.md` and `docs/decisions/*.md`) into an OKF knowledge base named `cartographer-docs`. The generator is a pure function of those files and of a git ref: no model, no Surveyor, no clock, so the same input gives the same bytes (a test generates twice and compares).

The shape follows what `search` and `graph_context` reward:

- Three maps under `data/`, taken from the two tables of `docs/index.md` and from the decisions: `using-cartographer/`, `internals-contributing/` and `decisions/`. A page file in no table is a generator error. The root presentation (`README.md`) and the contribution guide (`CONTRIBUTING.md`) are a declared, ordered input list of the generator, not a special case, and `docs/index.md` is the page `internals-contributing/docs-map` with its two tables replaced by the map indexes.
- A page is an expanded concept (`map/page/index.md`) whose sections are its satellites (`map/page/section`, the deepest id the data plane allows). Its index lists every section, sub-sections indented, so the curated-index check holds by construction.
- A section is the H2 unit; while it exceeds 8 KiB and has deeper headings it is split at the next level, then the next; a leaf still above 16 KiB is cut at blank lines outside fences and tables into parts of at most 8 KiB, each linking its neighbours. Headings are found by a fence-aware line scanner (a `#` in a fenced block is no heading); no markdown dependency is added.
- A unit's title is its heading path joined by " — ", page title first, and its body starts with that same string as its H1. Slugs are the ASCII, lowercased heading path, de-duplicated with `-2`, `-3`; the GitHub anchor of each heading is kept separately for anchor resolution and for the pinned `source_url`.
- A decision is one leaf concept (`decisions/d193-…`, the file stem lowercased), never split, its body verbatim minus the YAML frontmatter, grouped by topic in `repodocs.TopicOrder` in the map index. `TEMPLATE.md` and pending `D-new-*` files are left out, and so is the generated decisions list block of `docs/decisions.md`.
- Every generated concept carries `provenance` (the repository path), `source_url` and `origin: external`: its text is a verbatim copy of a repository file, which is what that marker says to the machine-path, open-marker and title-length checks. No `timestamp`, so there is no decay finding and no nondeterminism.

The units, not the pages, are the retrieval unit: a hit whose title and first lines say what it answers is the answer. The rest of the plan builds on this model: links rewritten to wikilinks and pinned URLs with the lint gate (next change), the embed of the docs in the binary, `cartographer docs` running the real read tools in-process over a private per-invocation materialization in a temporary directory (not in memory, because the read paths work on directories and write per-KB state; not a shared cache, because two concurrent invocations would race on that state), the retrieval eval, the `cartographer-docs` skill and the hosted demo serving the KB beside the astronomy one. The embed is one module-root Go file so the root pages come in; it adds about 3.6 MB to the binary, stored raw.

This supersedes the guestbook of D800 (the demo serves the documentation KB instead); the static Atlas's live source of D800 stands.

**Links.** Relative `.md` links and `#anchors` of a page become `[[id|label]]` wikilinks (root-relative ids are base independent); an anchor resolves to the unit that holds the heading, even one inside a section that was not split, and an anchor with no heading is a generator error naming file and line. Any other link into the repository (code, `docs/openapi.json`, `docs/schemas/`, a directory, an image) becomes the pinned GitHub URL (`blob`, `tree` for a trailing slash) at the version tag when the build's version is exactly `vX.Y.Z` or `vX.Y.Z-rc.N`, else `main`; external URLs are untouched; code spans and fenced blocks are not scanned and a title after a destination is dropped. A second link to the same target in one paragraph is written as its label, which keeps `repeated_link` quiet. Every `D<n>` in the prose of a unit that has a decision file ends up in one appended line, `Decisions cited: [[decisions/d<n>-…|D<n>]], …` (ascending, at most 12, not the decision itself, not one the unit already links), which gives `graph_context` the why-trail and leaves decisions as leaves.

Three rules keep the lint clean without touching a page. Wiki-link syntax shown in prose (`[[a]]`) is put in a code span. The `{{repo:…}}`/`{{path:…}}` placeholders the docs cite as examples are declared in a generated `paths.yaml`. The generated decisions list of `docs/decisions.md` is replaced by the decisions map, whose ten topics are concepts (`decisions/topic-<slug>`, type `DecisionTopic`) that list their decisions: the `#topic` anchors of the dropped list resolve to them, and each decision ends with a `Topic:` link, so no decision is an orphan. `internal/docskb` generates the KB from the real docs in `go test` and fails on any lint finding but the four about the docs' own prose.

**Why.** An agent asked how workspace binding works could not answer: the answer is in `docs/configurator.md` and `docs/sync.md`, which an agent cannot consult. More skill prose would drift from the docs; a KB that is a function of the docs cannot. Small units make `search` precise and `graph_context` cheap, and the page hub plus the `Part of` line put the neighbouring sections one hop from a hit.

**Alternatives rejected.**
- Generation by a model or the Surveyor: not reproducible, costs a provider, and drifts silently.
- Compressing the embedded docs: needs a generated artifact (committed, or a pre-build step the release tooling does not have).
- One concept per page: a 200 KB page is not a search hit.
- H2 units only: five H2 sections of the real docs exceed 90 KB each.
- A shared cache directory for the materialization: concurrent invocations race on the per-KB state files the read tools write.

**Consequences.** A docs page in no table of `docs/index.md` fails the generator. The size caps (8 KiB and 16 KiB) are variables only so that the tests can lower them; a block no blank line can cut stays whole and is named in the size test. A heading rename changes a concept id. The later changes of the plan complete this record with the link rules, the embed, the runner and the eval.
