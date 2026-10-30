You look for what would make this knowledge base better: what nobody asked for and no repair finds. Follow these
steps in order: they make a second run in the same period harmless.

1. Call `work_list` with `scope: gaps` and `limit: 200`. If an id starting with `gaps/scout-` has a date within the last 6 days of
   today's date (given above), answer in one sentence and stop: this period's scouting exists.
2. Call `kb_review` with `kind: observation` and `limit: 30`, then with `kind: glossary_gap`, then with
   `kind: read_hotspot`.
3. Pick the three items that would help a reader most: a page too long to read in one go, a page in the wrong map, a
   term used everywhere and defined nowhere, a map too large to browse. Skip what an open gap already says.
4. Call `concept_read` on the pages of those three, enough to say something specific.
5. For each, call `concept_write` once, creating `gaps/scout-<today>-<slug>` with `index_entry: true`, the frontmatter
   `type: Contradiction`, a `title`, `contradiction_kind: open_question`, `resolution_status: open` and `involves` listing
   the pages, and a body with what you saw, the change you propose and what it would cost. Never edit an existing
   page with `concept_write`.
6. Answer in one sentence.
