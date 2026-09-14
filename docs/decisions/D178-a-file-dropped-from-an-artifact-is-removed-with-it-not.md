---
topic: sync-provisioning
---

# D178 — A file dropped from an artifact is removed with it, not stranded

**Decision.** When a multi-file artifact is rewritten, `Apply` removes the files
it owned in the previous lock and no longer declares, through the ordinary prune.
`doctor` reports — and never deletes — files inside a managed directory that no
lock entry accounts for.

**Context.** Two paths combined into a silent leak. `copyArtifactFiles` wrote
what the artifact currently carries and never removed what it no longer did;
`Apply` then rebuilt `newManaged` from the write, dropping the entries for the
files that disappeared. The result stayed on disk **and** left the lock, so
pruning, `ComputeDiff` and `doctor`'s on-disk verification were all blind to it.

Beyond tidiness: for a skill or an agent, an orphan inside a live artifact
directory is still read by the agent — a reference file deleted from a KB skill
kept being loaded indefinitely, with no drift signal — and an orphaned hook
script may still be invoked by a registration that outlives it.

- **In `Apply`, not in a cleanup command.** A sync that leaves a known orphan
  behind and relies on a later `doctor --repair` is a sync that lied about being
  in sync.
- **Only what this artifact previously owned.** The removal set comes from the
  previous lock's entries for that `kind+name`, minus what was just written, and
  is further restricted to paths under the artifact's own destination directory.
  Deleting by directory listing would take a file the user placed there — the
  guarantee the instructions prune is already careful to keep — and would
  mistake a hook's generated plugin file, written elsewhere, for a dropped one.
- **Reported like any other prune.** It goes through `PruneManaged`, so empty
  directories are cleaned and the removal shows up in `printApplySummary` and in
  the `--dry-run` plan. A silent deletion is not an improvement over a silent
  orphan. Under a dry run the comparison uses the artifact's declared files
  rather than the simulated principal path, or the plan would list every other
  file of the artifact as removed.
- **Pre-existing orphans are reported, not deleted.** Files stranded by earlier
  versions are in no lock, so nothing can prove Cartographer wrote them: `doctor`
  names them with a remedy and leaves them alone. They disappear naturally at the
  next sync once the artifact owns the path again.

**Deliberately not implemented.** The plan also proposed removing the files of an
artifact that becomes **unauthorized** mid-update, by analogy with the
force-pruned unauthorized `mcp` entries. That analogy does not hold: an MCP
approval is explicit and revocable per hash, so its withdrawal is an intentional
signal, whereas an unsigned KB skill is unauthorized on **every** sync that does
not pass `--auto-trust` and has no persisted `trust`. Applying the rule there
would delete a legitimately installed skill on every ordinary sync. The
unauthorized case therefore keeps today's behaviour: the artifact stays on disk
and is reported as needing approval.

**Consequences.** The first sync after release removes files that were orphaned
**and** are re-owned by a rewritten artifact; older orphans are reported by
`doctor`, not deleted. Both belong in the release notes.
