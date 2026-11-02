---
topic: control-plane
---

# D1317 — kb_repair applies several checks in one call

**Decision.** `kb_repair`'s `check` is a string, a list of strings, or `"*"` (every `lint.FixableChecks`). The server runs the checks in `lint.AutoRepairOrder` order, then the other fixable ones, planning each against the tree the previous one left, and commits once per call: `kb_repair: <k> checks (<n> applied)`, one `<check>: <n>` body line per check. `dry_run` still defaults to true. `kb_status` gains `conformance.repair`, a fixed string naming the call, present only when `repairable` is not empty.

**Why.** A usability test with only the MCP server showed mechanical repair costing two calls per check (dry run, then apply): 12 calls on a KB with 257 findings. `kb_status` already lists the checks to run; what was missing was a call that applies them all. A caller's order must not change the result because fixes depend on each other (D352), so the order is the server's. The handler never commits (`gitWrap` does), so per-check commits are not possible; one commit per call keeps `repair_revert` working on the whole call, at the cost of a coarser undo (repair a check alone to revert it alone), the same model as the background repair.

**Alternatives rejected.**
- Applying by default for `"*"`: an explicit `dry_run: false` is what keeps a destructive call deliberate.
- One commit per check: needs the handler to commit, which double-commits or deadlocks.
- Excluding removals from `"*"` as the unattended path does: an explicit call removes (D1197) and `"*"` is explicit; the response reports `removals` per check instead.
- A cache for the whole-KB lint each check plans with: not needed to meet the goal, can be added if a measurement asks for it.

**Consequences.** The single-string response is unchanged and the CLI and Atlas parse it as before. A multi response has its own shape (`checks`, `applied_total`, `planned_total_all`, `checks_run`). The per-check function (`repairOneCheck`) neither logs nor sets the commit subject: its caller does, once per call. Validation of the names is all-or-nothing before any write.
