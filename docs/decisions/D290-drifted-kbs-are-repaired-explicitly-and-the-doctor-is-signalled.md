---
topic: control-plane
---

# D290 — Drifted KBs are repaired by an explicit tool call, and `kb_status` signals when to

**Decision.** `kb_repair(check, [scope], [dry_run], [limit])` applies the mechanical `fix` that lint attaches (D289) to one check's findings, dry-run by default, one commit per call. `kb_status` gains a `conformance` object (counts by severity, `fixable`, `last_doctor`, `doctor_suggested`) so the KB says when it needs care. Judgement fixes are the bundled `kb-doctor` skill's job.

**Why.** On a real 220-concept KB one synonym field appeared in 205 concepts: renaming it through `concept_write` is 205 calls and 205 commits, and nobody does it. Separately, the debt (retired links, misfit concepts) accumulated unseen because `gate_check` defaults to floor `warning` and is scoped to changed concepts. The server can apply a rename deterministically; it cannot decide how to reword a link (D14), so that stays with an agent and the operator.

**Alternatives rejected.**
- A stored KB "standard version" plus numbered migrations: a KB can regress after being migrated (an agent writes a synonym again), so a stored level would lie. Re-running the idempotent D289 checks is the truth; a future convention change adds a check with a `fix`, which is the whole migration mechanism.
- Repairing on upgrade or startup: a doctor that silently fixes things is unpredictable (the D143 stance). Repair is an explicit call, dry-run first.
- Triggering through provisioning: the signal is `kb_status`, and the skill and client decide when to propose it. Scheduling stays client-side, and `internal/provisioning` stays untouched.
- Judgement fixes (rewording links, moving concepts) in the server: breaks D14.
- Hiding `kb_repair` in `advancedToolNames`: a descriptor-bound host can only call what `tools/list` advertises (D123), and the skill needs it.

**Consequences.**
- `kb_repair` with `dry_run` never writes; a repair commit touches only what the fixes name, including the `_map.md` contract a rename affects; a concept changed between listing and applying is skipped (`stale_write`), not overwritten. A skipped concept keeps the old key while its map's contract already names the new one until the check is re-run: the response lists it.
- `lint.FixableChecks` is the tool's contract; a test fails when a check emits a fix that is not listed.
- The commit subject carries the number of concepts, which only the handler knows: `ToolResult.CommitSubject` lets a wrapped handler replace the subject gitWrap derives from the arguments. The handler cannot know the commit SHA, so the response has none; the commit is in the history and the audit log.
- `last_doctor` reads a `log.md` entry containing `kb-doctor`, which the skill's closing `log_append` writes; `kb_repair`'s own entry does not carry it, because a mechanical pass is not a doctor session.
- Cost: `kb_status` now runs a whole-KB lint. Measured with `BenchmarkKBStatusConformance1000` (1,000 concepts in 10 maps, lint plus the log scan) at about 70 ms per call on a laptop, under the 200 ms threshold set for caching, so it is computed on every call. If the lint grows past that, cache it on the graph cache generation (D241).
- The counts come from the caller's visible concepts only, as D226 requires.
