---
topic: data-plane
---

# D310 — A link to an expanded concept's index.md is the concept, normalised in the graph

**Decision.** The link graph stores a link target spelled `<concept>/index` under `<concept>` when that concept exists (`buildGraphView`); `ExtractLinks` and `RewriteLinks` are unchanged. A new info check `index_link_form` flags the spelling and `kb_repair` rewrites it: a markdown href to the relative `<concept>.md` (`rebase_link`), a wiki-link to the bare ID (`rewrite_wiki_link`, new). A `broken_link` that would resolve to the concept itself carries a `rebase_link` with an empty `to`, which drops the link and keeps the label.

**Why.** `conceptFiles` gives an expanded concept's `index.md` the ID of its directory, so `map/c/index` was never a node: backlinks missed the edge, orphan and `cut_concept` fired on a linked concept, reciprocal-link detection failed. D149 kept `ExtractLinks` literal so as not to break `expanded_ambiguous`, but that check stats files and never reads `ExtractLinks`, so the objection does not reach the graph. The real constraint is `RewriteLinks`, which must resolve as `ExtractLinks` does (D248) to rewrite the right href, so the normalisation sits one layer above, where edges are inserted.

**Alternatives rejected.**
- Normalise inside `ExtractLinks`: would force the same in `RewriteLinks`, which needs the original href form to compute the new relative path.
- Only the lint check, no graph change: leaves every graph tool wrong until each KB is repaired.
- A new `drop_self_link` fix kind: an empty `to` on `rebase_link` stays inside the existing vocabulary.
- Markdown to wiki-link rewriting in expanded indexes (the plan's first idea): needs label handling and a second fix kind; the relative `<concept>.md` is valid from the expanded base and is one mechanical rewrite.

**Consequences.** A target whose parent is no concept stays as written (a genuine broken link). `checkCuratedIndex` keeps its own `CutSuffix("/index")` because it reads raw `ExtractLinks` output. Graph topology changes for KBs that use the `/index` spelling: orphan and `cut_concept` findings may drop, `index_link_form` findings appear.
