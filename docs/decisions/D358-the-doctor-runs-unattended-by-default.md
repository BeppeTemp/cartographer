---
topic: control-plane
---

# D358 — The kb-doctor session runs unattended by default

**Decision.** Per KB, `doctor_mode: unattended | assisted` (default `unattended`) says who runs the
kb-doctor session the server proposes. Unattended, the nudge tells the agent to run it now, decide
every item under the skill's rules, record what only a person can answer as a gap
(`missing_context` / `open_question` Contradiction plus a `lint_ignore` citing it) and report in the
closing log entry. The session is due after 1 day (14 assisted), the nudge window is per MCP
session instead of per process, and the budget is `doctor_budget` review items (40; 10 assisted).

**Why.** The deterministic half cannot make judgement calls, and the old loop was designed around an
operator: one nudge per process per day, a 14-day interval, 10 decisions per session. A KB nobody
tends never converged, and an imported one with hundreds of findings never would. The server still
decides nothing (D14): the agent decides from the KB, every write carries a `reason`, and commits
carry only their own changes (D357), so the audit trail is the transparency. The cost is that a
default changes: agents act where they used to ask.

**Alternatives rejected.**
- A headless executor inside the server: the server has no model (D14); client, model and
  credentials are the operator's. `docs/loop.md` documents a scheduled headless client session.
- Keep the 10-decision cap unattended: it measures questions asked, and an unattended session asks
  none; the budget counts work.
- Keep `assisted` as the default: the opt-in would be the exception nobody sets, and the KBs that
  need it most are the ones nobody tends.
- A persisted nudge window: a restart costs one extra notice at most, not worth a state file.

**Consequences.** `doctor_mode: assisted` restores the previous behavior exactly (interval and text).
An explicit `doctor_interval` wins over the mode's default. The skill's "never delete a concept,
never rename a map's folder, never invent content" is the unattended invariant: it stays verbatim.
A transport with no session ID shares one nudge bucket, the former per-process behavior. A gap that
duplicates an open one is updated, not repeated (the skill searches first).
