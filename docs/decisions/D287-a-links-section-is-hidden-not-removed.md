---
topic: control-plane
---

# D287 — A trailing links section is hidden by the Atlas, never flagged as such; its duplicates and bare lists are

**Decision.** Many KB templates end a page with a links section
(`## Collegamenti`, `## Links`, `## See also`). The Atlas reading panel hides it
only when it is links-only and every target is already among the concept's
outbound links; otherwise it is rendered like any other section. Lint adds two
`info` checks about it, both suppressible with `lint_ignore`: `duplicate_link`
(a link written both in the text and in the section) and `bare_link_list` (a
section of links with not one word on why). The section itself is not a
finding.

**Why.** In the Atlas the section read as a duplicate of the Links tab, and the
first instinct was to drop it from the KBs. Measured on real KBs it is the
opposite of a duplicate in the files: in a ~700-concept work KB, 575 of 591
pages with the section hold links that exist nowhere else in the page — about
2,300 links, most of the graph's edges. Removing it would empty the graph and
the backlinks. The duplication is real only in two places: on screen (fixed by
hiding it where the tab covers it) and where a page links the same concept in
the prose and again in the list (410 pages in the same KB) — which is what
`duplicate_link` reports. A list with no reasons is the other weakness worth
naming: a reader cannot tell a dependency from a loose association.

**Alternatives rejected.** Removing the section from templates and pages —
destroys most edges unless every link is first rewritten into the prose, and a
bulk LLM rewrite of thousands of links is the kind of change this project
avoids. A lint check on the section itself — ~760 findings of pure noise on
KBs that follow their own template. Warning severity — these are judgements,
not broken contracts, and must never fail a gate.

**Consequences.** Moving a KB from trailing sections to links in context is a
content decision for that KB's templates, made gradually; `duplicate_link`
points at the pages where one of the two copies can go today. The heading
names recognised are shared by the lint check and the UI helper and must stay
in step.
