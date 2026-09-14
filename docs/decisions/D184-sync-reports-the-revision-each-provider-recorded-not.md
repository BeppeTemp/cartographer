---
topic: sync-provisioning
---

# D184 — `sync` reports the revision each provider recorded, not the one it fetched

**Decision.** `printSyncRevisions` and `commonRevision` (`cmd/cartographer/sync.go`)
take `map[string]provisioning.AppliedResult` — `materializeForProviders`'s return
value — instead of the pre-projection `map[string]provisioning.Manifest`, and read
`AppliedResult.NewLock.AppliedRevision`: the same value `Apply` writes into the
lockfile and `status` compares against, rather than recomputing `FilterForProvider`
independently at the call site. The dry-run path takes the same values: `Apply`
fills `NewLock.AppliedRevision` from the post-filter manifest whether or not
`DryRun` is set, so a plan and the real run that follows it name the same number.

**Context.** `printSyncRevisions` was handed `manifests`, the map
`manifestsForProviders` returns *before* per-provider projection. The
projection — `FilterForProvider`, which drops kinds a provider has no
destination for and recomputes the revision over what remains ([D170](D170-selection-before-the-merge-each-provider-is-projected.md))
— happens one level down, inside `materializeForProviders` → `Apply`. For a
provider that supports less than the full kind set — `hermes` is the sharpest
case, only `skill` ([D141](D141-hermes-is-a-supported-provider-that-receives.md)) — `sync` printed a revision no other
command would ever produce: `status` reads `lockfile.applied_revision`, itself
set from the *filtered* manifest at `Apply` time. Reproduced on a
`hermes`-only client, `sync` and `status` disagreed on every single run with
nothing actually out of sync — the [D147](D147-every-reported-write-is-observed-never-intended.md)
failure shape again (output derived from the command's intent, not its
outcome), on the very provider D147's own writeup already names.

**Rationale.**

- **Read the recorded value, don't recompute it.** `materializeForProviders`
  already returns the applied result per provider; sourcing the printed
  revision from `AppliedResult.NewLock.AppliedRevision` leaves exactly one
  place that computes "what does this provider's revision look like" —
  inside `Apply`, via `FilterForProvider` — instead of a second, independent
  computation at the reporting site that could silently drift from it again.
- **`commonRevision`'s agree/diverge logic is unchanged.** Only the source of
  the numbers it compares changed; unsupported-kind filtering is simply a
  second cause of the same per-provider divergence the function already
  handles for bindings ([D170](D170-selection-before-the-merge-each-provider-is-projected.md)).
- **Dry run and real must not diverge.** `Apply` computes
  `NewLock.AppliedRevision` from the filtered manifest before branching on
  `DryRun`, so `--dry-run` and the real `sync` that follows it name the same
  revision for an unchanged manifest — the invariant D147 established for the
  verb (`would sync to`) now also holds for the number it precedes.

**Consequences.** `fix:` — output only; no configuration, CLI or MCP surface
change. A provider with unsupported kinds now prints the same revision
`status` reports for it.
