---
topic: control-plane
---

# D302 — Work is a view over open states and unchecked items, not a type

**Decision.** A work item is either a concept in an open phase for its map, of
any type, or an unchecked `- [ ]` item in any concept, whatever that concept's
status. `work_list` (and the Atlas Work panel through `GET .../work`) returns
them read-only, filtered by `concept_list`'s `where`. It returns and orders by
the frontmatter fields the caller names, and never interprets them.
`kb_status.work` counts them. A map may name a `work_map`. Work kept elsewhere
in that map with no link into it then becomes the `scattered_work` review kind.

**Why.** The KBs measured for this keep work in two generic shapes. In the
larger one there are no `Task` concepts at all, 127 journal entries in an open
status and 315 open checkboxes spread over 199 pages. A planner keyed on a type
would see none of that, and making it visible would need a migration. Open
phases (`open_statuses`, D297) and checkboxes already exist as concepts the
server knows. Priority, owner and due date are each KB's own vocabulary. The
server stays generic by sorting on them as strings rather than modelling them.

A read-only view keeps every change to work behind the ordinary write tools and
their gates. Per-principal assignment and permissions on work belong to the
permission work planned for 0.19. Scattered work is flagged only where a
contract says where work belongs. A KB that keeps its work in journals gets no
noise.

**Alternatives rejected.**
- *A `Task` type with a schema*: invisible to KBs that track work in journals
  and checklists, and it forces a migration before any value.
- *Write operations on the view (claim, close, move)*: these would bypass the
  write gates and need the permission model that does not exist yet.
- *Interpreting `priority`/`due`*: every KB names and scales them differently.
  Ordering by caller-named keys gives the same value without a vocabulary.
- *Flagging every checkbox outside a backlog*: noise for KBs whose journals are
  where work lives on purpose, so `work_map` is opt-in.
- *Trimming other tool descriptions to fit `work_list` in 22 KiB*: D301 had
  already removed the lists that grow. The remaining text is calling rules. The
  `agent` `tools/list` went from 22,490 to 23,190 bytes, so the budget is
  23 KiB, and `work_list`'s own description is capped at 350 characters by a
  test.

**Consequences.** The collector (`lint.Work`) is pure and shares `openPhase`,
the checkbox pattern and code masking with the lifecycle lint, so the view and
`stale_open`/`closed_with_open_items` cannot disagree, with one deliberate
exception: by default the `draft` family is not work. Tried on a real KB, the
view listed study pages in `draft` as open work. A draft says a page is still
being written, not that something in its subject is pending, so it is work only
where a map's `open_statuses` lists it; it still goes stale for `stale_open`,
and its unchecked items stay work. `total` counts entries, for paging, while
`open_concepts` and `by_status` count only open-phase concepts: a closed concept
listed for its items would otherwise show its closed status as work. The cached view is keyed
on the lint inputs and the day, because ages move with the calendar. Visibility
is applied outside the cache, and a test pins that a hidden concept never
reaches entries or counts. `work_list` is part of the `agent` core set: a
descriptor-bound host cannot call a hidden tool (D123). `scattered_work` ranks
after `promotion_candidate`, and `lint_ignore` dismisses it.
