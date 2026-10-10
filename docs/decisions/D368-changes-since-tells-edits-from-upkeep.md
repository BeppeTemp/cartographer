---
topic: control-plane
---

# D368 — `changes_since` tells content changes from the server's upkeep

**Decision.** Each concept in `changes_since` adds `last_edit_at`, the time of
its newest commit that is not an `auto-repair` commit (omitted when only
upkeep touched it), and `added: true` when its file was created in the range,
even if edited since. The Atlas Activity panel classifies a change as new,
edited, reorganised or maintenance from them, places it on the day of its last
content change, and hides maintenance unless asked.

**Why.** Since the background repair (D323, D355) a single run touches
hundreds of pages in one commit; the panel counted every one as an edit dated
the night of the run, so "what changed this week" read as "the repair ran".
`ops` is capped at five per concept, so a concept edited and then repaired
several times lost its edit from that list; the fields come from the full
commit walk the tool already does.

**Alternatives rejected.**
- Filter upkeep in the UI from `ops`: the cap drops the evidence.
- Exclude auto-repair commits from `changes_since` altogether: an agent asking
  what changed must still see a repair it may want to revert.
- Treat any commit whose reason starts with a tool name as content: the
  subject prefix `auto-repair` is the server's own and stable; a manual
  `kb_repair` stays a change someone asked for.

**Consequences.** `last_at` keeps its meaning (newest commit of any kind); a
client that ignores the new fields sees no difference.
