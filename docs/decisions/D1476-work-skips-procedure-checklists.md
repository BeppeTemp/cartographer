---
topic: data-plane
---

# D1476 — Work skips the checklists under a map's procedure headings

**Decision.** An unchecked `- [ ]` item under an H2 that matches the map's `procedure_headings` (default `procedure`, `steps`, `how to`) is not a work item: `work_list`, the Atlas Work view and `kb_review`'s `scattered_work` skip it, as `closed_with_open_items` already did (D313). An H3 or deeper heading stays inside the procedure section; the next H2 or an H1 leaves it. Every other checkbox is still work whatever the page's status (D302).

**Why.** A page and the Work view disagreed on the same lines: a plan or runbook whose template carries a checklist to repeat each time (a closing checklist, the steps of a procedure) listed every line as open work, so the view filled with items nobody had to do, and on a real KB a fifth of `open_items` were template checklists. The lint already had the contract key that says "this section is a reusable checklist"; Work ignored it. It costs a map that writes real to-dos under a heading named like a procedure: those items leave the view until the map narrows its `procedure_headings`.

**Alternatives rejected.**
- Count checkboxes only in open-phase pages: many KBs keep their only work signal as action items in `active` pages (meeting notes, journals), and D302 promised nothing has to be retyped for the server to see work. The Atlas already groups a page listed only for its items apart, under "unchecked items".
- A new contract key for Work alone (`checklist_headings`): two lists saying the same thing about the same sections would drift, and a page would be closed by one rule and open by the other.
- Read the checklist sections from the map's templates: a template declares the shape of a page, not which of its lists are work; the key would apply only to KBs with templates, and a map without them would need another mechanism.

**Consequences.** `workItems` takes the map contract; any future reader of unchecked items must pass it, so the three readers stay aligned. A KB sees a change only where its pages have checklists under the default headings or under headings it declared. `open_items` and the stale extension of `stale_open` to old items drop accordingly.
