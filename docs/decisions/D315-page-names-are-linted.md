---
topic: data-plane
---

# D315 — Page names are linted: title and heading agree, titles are labels, map titles are short

**Decision.** `title_h1_mismatch` (warning, fix `sync_h1`) fires when a concept's
frontmatter `title` and its first `# ` heading both exist and differ; the repair
overwrites the heading with the title. `title_quality` (info, no fix) fires on a
decorative character, a title over `title_max_length` (default 100, per map), a
lifecycle word when the concept has a `status`, a map's `forbidden_title_terms`,
and a `YYYY-MM` slug outside a `kind: journal` map. `map_naming` also flags a
map title poor on its own (over 30 characters, a subtitle over 20, no
resemblance to its folder).

**Why.** On a real KB about a seventh of the concepts had a title and a heading
that had drifted apart, and nothing reported it. The title is what `concept_list`,
search, graph tools, generated indexes and the Atlas show, so the heading is the
rendering and the one to rewrite; the reverse would make every reader of a concept
parse its body. Quality rules are info because a long or decorated title breaks
nothing, it only makes listings noisy, and they have no fix because the right
wording is a judgement. A set of map titles consistent with each other can still
be individually bad (D304 compared them only to each other).

**Alternatives rejected.**
- Folding the mismatch into `missing_title`: a different condition with a different fix.
- The heading as source of truth: every tool that reads a concept would need its body.
- A mechanical fix for `title_quality`: it would invent wording.
- `title_max_length` as a positive-only key like the cost keys (D301): `0` has to
  mean "off" for a map of long names, so `0` is stored and the update removes the
  key with a negative value.
- Status words matched as substrings: `active` would flag "proactive". They match
  whole words.
- Individual map-title problems as lint findings: renaming a map is a scheme
  decision, so they stay a review item.

**Consequences.** `sync_h1` replaces the heading with plain text, so formatting
in the old heading is lost; the check compares the raw heading text, so a bold
heading over a plain title is reported and repaired. The folder-mismatch signal
fires for a map titled in another language than its folder; the agent judges it
from the evidence. The tools/list size budget moved to 25.5 KiB for the two new
`map_update` parameters.
