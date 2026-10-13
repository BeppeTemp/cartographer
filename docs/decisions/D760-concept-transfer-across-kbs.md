---
topic: control-plane
---

# D760 — `concept_transfer` is a new tool that moves concepts between KBs under both git locks, target first

**Decision.** `concept_transfer(target_kb, moves[])` moves concepts, with their
assets, from the KB it is called on to another KB mounted on the same server. It
is a tool of its own, not a `target_kb` on `concept_move`. It takes both KBs'
git locks in one global order (the KBs' root paths), validates everything, writes
and commits the **target**, then removes from and commits the **source**, then
pushes both. A target-step failure discards the target tree and leaves the source
alone; a source-step failure discards the source tree and undoes the target
commit with a **compensating revert commit**, never a reset. Links to a moved page
that stay behind in the source become plain text (the label is kept). Files are
copied, byte for byte, never renamed. The source commit carries `Moved-To: <kb>`,
the target commit `Moved-From: <kb>`.

**Why.** Nothing touched two KBs at once, so moving pages meant re-emitting each
through the model, re-uploading binaries as base64 and deleting the originals.
`concept_move`'s policy class, audit keys and `gitWrap` all assume one KB, and
tool names and semantics are a public contract (D101): widening it would have
changed what a grant on it means. Target first means the content exists on the
target before it disappears from the source, so a crash between the two leaves a
duplicate, never a loss. The lock order is global because `k.mu` is not reentrant
and `A → B` can run beside `B → A`. Copy, not `os.Rename`, because two KB roots may
sit on different filesystems (`EXDEV`) and a rename would remove the source
before the target is committed.

**Alternatives rejected.**
- `target_kb` on `concept_move`: one tool with two policy shapes and a changed audit record.
- A cross-KB link syntax for the links left behind: it would have to be understood by lint, the graph, the Atlas and every client, for a feature used rarely.
- Source first, then target: a failure in between loses content.
- A hard reset of the target after a source failure: the target commit may already be fetched, and history is append-only.
- Applying the write gate (D350) to a transfer: it rolls a tree back after the fact, which with two KBs and a commit in between has no coherent meaning. Findings are reported per KB instead and the tool takes no `accept_findings`.

**Consequences.** `concept_transfer` is the one write tool outside `gitWrap`; it
reuses its sync, trailer and push steps (`syncInForWrite`, `commitTrailerBlock`,
`pushAfterCommit`). It needs a server that mounts both KBs (`Deps.KBByName`;
single-KB stdio answers that it cannot), both KBs git-backed with auto-commit
(the undo path is git), and on the routed endpoint a target inside the
connection's KB set. The caller needs a whole-KB write grant on the source and a
write grant on each destination slot of the target. Placeholder keys the target
does not declare, sops material and `secret_refs` that do not travel are
reported, not refused. Skills (`artifact_move`) are out of scope and a follow-up
on the same mechanism.

**Safety rules from review.** Success of a commit is decided by HEAD moving, not
by the SHA `CommitOpAs` returns; when the outcome is unknown nothing is discarded
or reverted. The target is reverted only after the source tree was discarded:
otherwise the target copy stays (a duplicate is recoverable, a loss is not). An
expanded concept holding gitignored files is refused. Links from pages that stay
to a moved asset are reported, not rewritten. Target-first push ordering is best
effort: a failed target push does not stop the source push, and debounced pushes
run per KB.
