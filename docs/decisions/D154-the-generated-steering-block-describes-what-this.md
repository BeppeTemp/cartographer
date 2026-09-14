---
topic: sync-provisioning
---

# D154 — The generated steering block describes what this client received

**Status: implemented (2026-08-28).** Amends [D65](D65-agent-tool-profile-and-compact-instructions-less-fixed.md) and [D141](D141-hermes-is-a-supported-provider-that-receives.md). Closes #175.

**Context.** The generated instructions described **intended** state rather than what provisioning
achieved on that client, so any partially-supported client got confidently wrong instructions.

`generateKBInstructions` emitted, whenever the KB declared any agent: *"Subagents installed by this
KB: <a>, <b> — their descriptions are in the client's agent registry: delegate to them the tasks
they cover."* The list came from `kbAgentNames(kbRoot)` — what the KB **declares** on disk. It could
not come from what the client received: the function has no provider argument and its only caller,
`BuildManifest`, runs exclusively server-side, where no provider is known. Meanwhile whether a
provider can receive an agent at all is a static fact the code already states —
`destinationMatrix["agent"]` marks `kiro` and `hermes` `unsupportedDest`. So a KB with two agents
synced to Claude Code and Kiro materialized them for the first and **not at all** for the second,
while both were told they were installed. On the reporting deployment the only agents in that
client's registry were stale files from an earlier bootstrap, still pointing at a decommissioned
source.

**Two corrections to the field report, made while implementing.**

*The omission was not silent, it was not persistent.* `printApplySummary` does print
`unsupported: agent/<name> … (kind has no destination for this provider)` for every artifact routed
to `Unsupported`. What is missing is persistence: the line appears only on a run where the artifact
enters the diff, `doctor` never reported the condition, and the steering sentence was wrong
regardless of any output — which is the part that misleads the model rather than the operator.

*"Written once per mounted KB" is a reporting defect, not three writes.*
`applyInstructionsGroup` accumulates every KB's snippet and calls `writeInstructionsBlock` **once**;
it then appends one `ManagedFile` per instructions artifact — i.e. per KB — and the summary printed
one line each. Nothing was rewritten N−1 times, so the fix belongs in the printer.

**Decision.**

- **The subagent sentence moves out of the hashed artifact and is emitted client-side, per
  provider.** There is an exact precedent in the same function: the D75 WP4 local-paths table is
  appended there, client-side only, and deliberately not part of the artifact hash. The sentence has
  the same nature — a statement about *this client*, not about the KB.
- It lists only agents whose kind has a destination for the provider, and only those actually
  authorized: an artifact awaiting approval was not installed, and the whole point is that the
  sentence describes what happened.
- **The rewrite trigger now fires on an `agent` artifact too.** The block's rendered content depends
  on the agent set while its artifact hash deliberately does not, so without this the sentence would
  go stale the moment an agent was added or removed. The test that asserted the hash changes when an
  agent is added now asserts the opposite, and names the trigger as the guarantee that replaced it —
  the invariant moved, it did not disappear.
- **A declared kind the provider cannot receive is warned about on every run**, computed from the
  manifest rather than the diff.
- **The wrapper becomes opt-out, not translated.** `instructions.md` may declare
  `<!-- cartographer: preamble: none -->` on its first line to suppress the three generated
  bullets — the redundant, hardcoded-English part. Deliberately **not** a localisation mechanism: a
  `language:` key would make Cartographer carry translations of its own prose forever, so the KB
  owns the prose instead. **The one-line routing sentence stays generated in every case** (KB name,
  server, archives): it is state, not prose, and no KB can write it for itself. The residual is
  therefore one English line rather than none, and this entry says so rather than claiming the
  language break is gone. The directive is honoured on the first line only, so a KB can document it
  without triggering it — the same trap as the placeholder syntax.
- **Reporting collapses per path for the instructions kind only.** `result.Written` keeps one entry
  per artifact, because the lockfile and drift detection depend on it; elsewhere two artifacts never
  share a path and a blanket dedup would hide a real bug.

**Consequences.** The steering block changes content on clients that cannot receive agents — the
false sentence disappears — so those clients rewrite the block once on the next sync, which is the
normal drift path. New KB-side directive; no config migration.
