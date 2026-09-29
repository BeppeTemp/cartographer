---
topic: control-plane
---

# D271 — `concept_delete` refuses to break inbound links unless `force` is set

**Decision.** `concept_delete` fails with `inbound_links:` and the list of linking
concepts (up to 20, then "and N more") when other concepts still link to the target,
unless `force: true`. With `force` it deletes and names the broken linkers in a warning.

**Why.** Deletion was the only structural write that knowingly degraded the link graph
without saying where; the agent learned which pages it broke only by running `lint`
later. The caller alone can decide whether to relink, supersede or accept the breakage,
and it can only decide with the list in hand. The inbound set already exists
(`KB.IncomingLinks`, D241). The cost is a breaking change: a caller that relied on the
silent delete must now pass `force`.

**Alternatives rejected.**

- Keep the generic warning: says nothing about which pages broke.
- Rewrite or remove the links automatically: guesses the caller's intent; `concept_move` already covers the rename case (D72, D248).
- A second flag (`allow_broken_links`): `force` already means "accept the destructive side effects" (it authorises deleting an expanded concept's assets), so two flags would split one idea.
- List every linker regardless of visibility: would disclose the existence of hidden concepts to a narrowed token.

**Consequences.** Only linkers passing `Visible` are listed or counted, so a hidden
linker neither blocks the delete nor appears in any message; `lint` still reports it to
a whole-KB principal. Self-links and an expanded concept's own satellites/index are not
inbound links, since the same call removes or preserves those files. A failure reading
the link graph fails the delete rather than guessing. Later checks (for example
provenance citations) extend this same gate.
