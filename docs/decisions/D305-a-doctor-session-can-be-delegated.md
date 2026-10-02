---
topic: data-plane
---

# D305 — A doctor session can be delegated, and the review groups a shared origin

**Decision.** The operator may hand a `kb-doctor` session over, or the KB's
`instructions.md` may say sessions run unattended. The agent then makes every
choice itself and reports afterwards. It decides from the KB, defers what only
the operator knows, never deletes a concept or renames a folder, and puts a reason
on every write. Separately, `zombie_work` groups the open concepts that share one
retired link (three or more) into one item. That item names the retired concept
first and is dismissed once on it.

**Why.** A first full session on a real KB turned 363 warnings into 0. The
operator's answer to the list of ten decisions was "do it all": asking per item
was the slowest part, and had no safety value once the operator chose the
recommendations. The guard rails that matter are the irreversible writes and the
facts the KB does not hold. Delegation keeps both. The same session showed 15 of
19 `zombie_work` items pointing at one archived backlog the tasks had been
migrated from: their origin, not their subject. The four real zombies (tasks
about components since retired) were buried under them. The cost of delegation is
a session no one approved item by item. The closing report and the per-write
reasons are what make it reviewable and revertible.

**Alternatives rejected.**
- *A server capability for autonomy*: the server never applies judgement (D14);
  who chooses is a property of the session, so it lives in the skill and the
  KB's own instructions.
- *Per-concept dismissal for each origin link*: 15 writes saying the same thing,
  and the next migrated task would raise the item again.
- *Ignoring links in a "Source" section*: section names are language and KB
  specific; a shared target is not.

**Consequences.** A delegated session can close work; it must search for what
replaced a retired subject before doing so, and record the reason in the
concept. `zombie_work`'s per-concept items now cover only retired targets a
concept does not share with two others.
