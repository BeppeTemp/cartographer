---
topic: sync-provisioning
---

# D148 — Provisioning refuses a symlinked destination

**Status: implemented (2026-08-28).** Closes #169.

**Context.** Materializing a KB's artifacts onto a destination that is a symlink followed
the link and wrote at the target. `os.WriteFile` on a symlinked path opens the *target* with
`O_WRONLY|O_TRUNC`; it does not replace the link. Reported from a field migration: the
client's skills directory was a symlink into an unrelated git checkout, and one
`cartographer sync` modified **23 files** there, each stamped with a provenance footer
declaring Cartographer as their source — false for those files, and in a shared repository
it lands inside someone else's commit.

The asymmetry is what made it a defect rather than a missing feature. The KB side already
guarded this: `internal/kb/asset.go` `Lstat`s every path component and refuses to traverse a
link. The client side performed 21 non-test `os.WriteFile`/`os.MkdirAll` calls across
`provisioning.go`, `hooksettings.go` and `bootstrap.go` with **zero** occurrences of
`Lstat`, `ModeSymlink` or `EvalSymlinks`.

Symlinked client-config directories are ordinary: a dotfile manager, a monorepo checkout, a
shared team directory, or an earlier bootstrap mechanism that linked skills out of a source
repository.

**Decision.**

- **Refuse, never resolve.** A symlinked destination is an error. Cartographer does not know
  what is on the other side and must not decide for the operator — and replacing the link
  with a regular file would be worse.
- The check is on the destination file **and every directory component under the base dir**
  (`ensureSafeDir`, mirroring `asset.go`'s component walk): the reported case was a symlinked
  *parent*, not a symlinked leaf. **The base dir itself is exempt** — it may legitimately be
  a link (a symlinked `$HOME`, a provider root from `BaseDirEnv`), so the walk starts below
  it.
- **The refusal is per artifact, not fatal to the pass.** One symlinked skill directory must
  not abort a whole sync, the same treatment `Apply` already gives an unsupported kind. The
  artifact goes to a new `AppliedResult.Refused` — distinct from `Unsupported` (the provider
  does support the kind) and from `NeedsApproval` (the artifact is authorized) — is reported
  as `refused: <kind>/<name>`, and is deliberately **left out of `Written` and out of the
  lockfile**, so the next sync retries and reports again. That is right for a condition only
  the operator can clear.
- The lockfile writes (`WriteLock`/`WriteLockFile`) are guarded too — a symlinked lockfile
  would make drift detection read and write someone else's state.
- `cartographer doctor` gains a `symlink` check over `ManagedDestinationRoots`, because
  `Apply` only mentions the condition on a run where the artifact is in the diff; afterwards
  it is invisible.
- **No git-awareness.** The stronger idea — refuse a destination that resolves inside a git
  repository other than the intended target — needs a repo-discovery walk on every write, and
  the symlink refusal already prevents the reported incident.

**Consequences.** Behaviour change: a sync that previously wrote through a symlink now
refuses that artifact, so an operator relying on a symlinked skills directory must repoint
the client base dir at the real location. `-dry-run` performs the same check and reports
`would refuse`, which is exactly when finding out is cheap.
