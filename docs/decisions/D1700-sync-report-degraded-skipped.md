---
topic: sync-provisioning
---

# D1700 — The sync summary reports degraded, skipped and unverified, filled by the caller from the resolver's notes

**Decision.** `AppliedResult` gains `Degraded`, `Skipped` and `Unverified`. The first two are the resolver's notes
(D1685) split by `provisioning.ReportNotes`; `Apply` receives only the projected manifest and cannot know them, so the
caller (`clientsync.go`) fills them from `ProjectionResult.Notes` right after `Apply`. `Unverified` is computed in
`Apply`, because it depends on what was installed and authorized: both agent capabilities of a native agent, and the
`hook.<Event>` of a hook, while their cell is `unverified`. Skill and mcp cells are never listed. `printApplySummary`
prints `degraded:`, `skipped:` and `unverified:` per provider, in the conditional on a dry run.

**Why.** A client that silently receives agents as skills, or silently lacks a skill, looks like a bug. The lines
repeat on every sync while true, like `unsupported:`, so they stay visible. `sync_apply`/`sync_check` are server-side and
do not project per client, so no JSON field was added (the plan allowed one if needed). An agent that would degrade but
cannot (name collision, no description) is reported as skipped, since it is not installed.

**Number.** Provisional: the PR number is not known yet; rename to `D<pr>-...` when it is opened (D672).
