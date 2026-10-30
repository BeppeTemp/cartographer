---
topic: control-plane
---

# D1436 — The worker's doctor works one review item at a time, as far as the operator allows

**Decision.** The `doctor` task no longer runs the `cartographer-kb-doctor` skill as one session. It first applies
`kb_repair` with no model, then reads `kb_review` itself and gives each item a rule covers a short loop of its own:
instructions embedded in the binary, the tools of that rule, at most 8 steps and 40,000 tokens. `tasks[].doctor_allow`
(`repair`, the default; `pages`; `structure`; `archive`) says which rules are on, and a guard fences each write to what
the item names. The signal to run is that something waits, not `doctor_suggested`. What a run examined is one line on
the log path `worker/<perimeter>/doctor`, read back to skip those items for 14 days. The worker never writes
`kb-doctor`. This partly supersedes D1261.

**Why.** The target of the worker is a small open-weight model, on any provider. One session sent the whole skill
(32 KB) and up to 33 KB of tool schemas on every call, which a small context window cannot hold. On a real KB such
runs spent 250,000 to 560,000 tokens each, were cut before closing, wrote the same pages in three runs in a row, and
in one case ended on the first long answer. Small loops cost the context of one item,
and the queue itself is the state, so no model-written note has to survive between runs. Those same runs also changed
the KB's structure unasked, and the maintainer of that KB judged the changes good: the point of the worker is an agent
that keeps a KB in order without being asked. So autonomy is a setting of the deployment and not a fixed line, and a
rule list narrowed to closing work was dropped when the real queue turned out to hold none of it: 121 items of other
kinds, so the task would have done nothing. The cost: a KB that turns everything on gets more commits nobody reviewed,
each revertible, and `structure` with `repair` drops values outside a declared vocabulary from pages.

**What the fences hold.** A KB's text reaches the model, so a page can steer it. The fences keep that from reaching
beyond the item: a rule always on changes frontmatter only, since a body edit is what a planted instruction would ask
for, and the rules that edit bodies need a word. `kb_repair` runs an allow-list, because a fix the server would not run
alone is one a person was meant to ask for, and a check added later must not start running on its own. A map takes only
the item's proposal, because that list decides what a later repair drops from pages. A carried fact lands only on a page
the entry links. The risk accepted: with `pages`, `archive` or `structure`, whoever can write a page can influence what
a run writes inside those limits, so those words suit a KB whose writers are trusted.

**Alternatives rejected.** Keeping one session and counting cached tokens at a fraction: it lowers the count on one
provider only and leaves a prefix no small model can hold. A resume page the model writes: it needs a map and a type on
every KB, the gate can refuse it at the moment of closing, the doctor would review its own notes, and an injected
instruction would survive into the next run. The worker writing the marker when its queue is empty: the kinds it never
touches would then be silenced for 14 days with the debt still there. A single autonomy level: it could not say "repair
and pages, never the maps". A deny-list for `kb_repair`: it fails open on every check added later. Keeping `doctor_suggested` as the gate: an operator's session would silence the worker for 14
days, and a worker that never writes the marker has no other use for it. Handing a model `map_update` as it stands: the
tool replaces `field_values` whole, so naming one field would drop the others; a local trial showed it. Exposing
`concept_merge` and `concept_archive`: `tools/list` does not advertise them, the allowlist intersects the list, and
both can wait for a first real need.

**Consequences.** `Next` reads the queue only while it moves: an hour's rest, in memory, follows a pass with nothing to
do. `doctor_suggested` stays true while there is debt, so an operator is still proposed a session for what the worker
leaves; `last_doctor` moves only with that session. A new rule needs a fence for each write tool it adds, and its
instructions and tools must stay small enough for a small model. What needs judgement across items is a prompt task's,
which may list `kb_review` and `kb_repair`.
