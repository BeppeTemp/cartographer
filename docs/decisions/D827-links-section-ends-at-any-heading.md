---
topic: data-plane
---

# D827 — A links section ends at the next heading of any level, and its repair keeps a heading the template requires

**Decision.** The trailing links section ends at the first heading of any level
after its own: a `###` under `## Links` is not part of it. Both link checks
(`duplicate_link`, `bare_link_list`) and the repair read that one boundary
(`locateLinksSection`). When `duplicate_link` or `reciprocal_link_item` empties
the section, its heading goes (D307) unless a deeper heading follows (it still
has children) or the page's template requires that H2 (by name or alias, and not
among the template's optional sections): the repair leaves an empty heading
instead of trading `duplicate_link` for `template_section_missing`. The two
messages say that a line of prose is enough to keep a links section. Amends
D287 and D307.

**Why.** A `###` after `## Links` is, in practice, a heading demoted from `##`
to get out of `template_extra_section`: prose, not links. Under the old boundary
(same or higher level) its links counted for both checks, and the repair could
drop a list line inside it as a "duplicate" — a destructive false positive. The
boundary lived twice, in `linksSection` and in `ReplaceLinkItem`, each with its
own regexp; one function now serves both so they cannot drift. Keeping a
required heading costs an empty section on a page, which no check flags (only
`empty_concept` exists); dropping it costs a finding the next lint reports. When
the section is emptied but `###` children follow, dropping its heading would
re-parent them under the previous `##`, so the heading stays.

**Alternatives rejected.**
- *Keep "same or higher level" and document it*: it keeps the false positives
  and the destructive repair.
- *Sub-headings directly under the links heading are link groups, deeper ones
  are not*: less predictable and harder to put in a message; no template or demo
  page groups its links under `###`.
- *Never drop a links heading in the repair*: the empty heading is noise on
  pages whose template does not ask for it.
- *Decide from the page's map alone*: the template is what names the required
  H2s; the map only says which templates it accepts.

**Consequences.** A `###` list under a links heading is text: its links are
checked against the section's items and it is never edited by the repair. A page
whose template requires the links section may carry it empty after a repair; a
line of prose under it is enough, and nothing asks for a list. `DropLinkItem`
and `ReplaceLinkItem` take a `HeadingRequired`; a caller that knows the page's
template passes `TemplateRequiresHeading`, a nil one requires nothing.
