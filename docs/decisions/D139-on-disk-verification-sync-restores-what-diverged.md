---
topic: sync-provisioning
---

# D139 — On-disk verification: sync restores what diverged locally

**Decision.** Every `Apply` verifies the managed artifacts against the filesystem, not only the
manifest against the lockfile, and rewrites the ones that diverged. The server is the source of
truth: a restore is a rewrite, never a merge, with no backup copy — the supported way to change an
artifact stays `artifact_write` on the owning KB, which is what the D138 stamp tells the reader.
`cartographer sync --no-heal` reports divergence and skips the restore; it is off by default,
because the default must match what the stamp promises. `AppliedResult` reports restores as
`Healed` (separately from ordinary writes: a restore discards someone's local change) and, under
`--no-heal`, as `Divergent`. `cartographer status` counts on-disk divergence as drift.

Verification uses `ManagedFile.MaterializedHash` (D138), not the manifest hash: on disk sit the
expanded, stamped and provider-translated bytes, which never equal the source hash. An empty value
means unknown — a lockfile written before D138 — and is reported but never healed: treating it as
drift would rewrite every artifact on every client at once on the first upgrade. Scope per kind:
`skill`/`hook` re-hash their own directory (so an extra file inside, or a lost executable bit,
counts as modified); `agent` hashes its single file; `mcp` and `instructions` live inside files
shared with the user and are checked for **presence** of their managed key or marker block only —
their surrounding content is never compared and never rewritten. A read error is a finding, never
fatal.

`executableModeDrift` disappears into this general path. It existed because a chmod alone did not
change the content hash, and its comment already admitted the gap it was patching: "before this,
Apply could complete an unchanged manifest without reading its source at all". Now the materialized
hash is computed on the **normalized** modes the write applies, so mode drift is ordinary content
drift — its test survives as the regression guard.

**Rationale.** Drift detection never looked at the filesystem, so once the lockfile said an
artifact was applied its files were never read again: a skill an agent "improved" in place stayed
improved forever, a deleted file was never recreated, and `status` reported in-sync throughout. This
is the mechanical half of what D138 addresses editorially — without it, the stamp's "local edits
are replaced on the next sync" was simply false. Presence-only checking for `mcp`/`instructions` is
what keeps the pruning guarantee intact: those blocks live in files the user also owns, and
comparing their whole content would either produce permanent false drift or license rewriting
someone else's file.
