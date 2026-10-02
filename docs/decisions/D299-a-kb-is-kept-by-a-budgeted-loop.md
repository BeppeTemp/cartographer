---
topic: control-plane
---

# D299 — A KB is kept by a budgeted loop the server proposes

**Decision.** Upkeep is a loop with three trust levels. **Mechanical** repair can run unattended, but only for the checks an operator lists in `kbs[].auto_repair` and only through `cartographer kb repair <kb> --apply`, a CLI command the operator schedules. **Proposals** (a vocabulary) and **judgement** (merge, move, close, promote) happen in a `kb-doctor` session (skill 2.0) with at most 10 numbered decisions, fed by `kb_review` (D298). The server **proposes** the session. `doctor_suggested` is now *debt and due*: any counted finding or review item, once `kbs[].doctor_interval` (default 14 days, `0` off) has passed since the last `kb-doctor` log entry. When the flag is true, the first write-capable, non-CLI caller of any tool gets one extra text block that asks the agent to propose a session to the operator. This happens at most once per KB every 24 hours.

**Why.** On five real KBs `doctor_suggested` was true and no doctor pass had ever run (`last_doctor` absent everywhere): the signal lived in `kb_status`, which no agent calls unprompted. D290's rule (any warning, or anything fixable) also meant the flag never went off on a real KB, so it carried no information. Mechanical renames on hundreds of concepts waited for a session where someone happened to ask, and judgement work was one heroic pass or nothing.

**Alternatives rejected.**
- *An agent or scheduler inside the server* (D143): a server that changes content by itself is unpredictable, and an agent needs the operator for judgement anyway.
- *`auto_repair: all`*: a release that adds a fixable check would enable it unattended without the operator ever seeing its dry-run plan, and D295 already added fixes that rewrite bodies. Each check is listed by name.
- *`kb repair --apply` writing the `kb-doctor` log marker*, as the plan first said: with the nudge, a daily cron would move `last_doctor` every day and silence the proposal for good while the judgement work stayed undone. A mechanical pass is not a doctor session (D290), and `kb_repair` already logs each applied check.
- *The nudge in the MCP `instructions` or a provisioned hook*: `instructions` are read at connect time and cached, so they go stale. A hook exists on some clients only. A text block in a tool result reaches every client.
- *Persisting the nudge window or a "declined" state*: declining needs no state, because the proposal comes back after the window. A restart re-arms it, which costs one notice at most.
- *A single exit code for "debt remains"*: on a real KB it would be set on every run, so a scheduler could not tell it from a failure or a clean KB.

**Consequences.**
- `summarizeConformance` takes the review total and the interval. `next_doctor` is reported, and `doctorStaleDays` is gone. D298's planned extra rule (review ≥ 20 for 30 days) was never shipped, because this rule replaces it.
- The CLI never receives the nudge: `client.Call` (`internal/client`) turns a multi-block result into a JSON array, which every CLI command would fail to decode. The server reads the clientInfo name the SDK resolved (`client.ClientName`), and `kb repair` also tolerates the array. A test drives the real client against an in-process server.
- `cartographer kb repair`, not `kb doctor`: `cartographer doctor` already checks client provisioning (D143), and "doctor" now names only the judgement session (`kb-doctor`, `last_doctor`, `doctor_interval`).
- The nudge is computed through the `kb_status` cache (D294). The cheap gates (error, tool, CLI, write access, interval, window) run first, and the window is claimed before the lint is read, so a KB without debt costs at most one lint per window on this path.
- Amends D290 (opt-in unattended mechanical repair; time-based `doctor_suggested`) and D298 (its `doctor_suggested` rule).
