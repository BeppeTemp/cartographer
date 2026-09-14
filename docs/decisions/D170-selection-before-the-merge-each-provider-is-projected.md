---
topic: sync-provisioning
---

# D170 — Selection before the merge: each provider is projected only its bound KBs

**Decision.** The client keeps the per-KB `sync_pull` responses unmerged
(`candidateSet`), and builds one manifest per provider: its bound KBs' responses
→ `SelectForSources` → `MergeArtifactsStrict` → `VerifiedManifest` →
`FilterForProvider` → `Apply`. `FilterForProvider` recomputes the revision,
`ManagedFile` records the artifact's `Source`, MCP entries are emitted per
provider, and `sync` gains a repeatable `--client`.

**Rationale.**

- **The filter had to move before the merge.** Filtering the merged manifest by
  source is wrong, and the failure is silent data loss rather than a visible
  error: `MergeArtifacts` deduplicates on `kind+name` and prefers a `kb:` source
  over `bundle`, so if `kb-A` overrides a bundled skill and a provider is bound
  only to `kb-B`, the merge keeps `kb-A`'s copy and the source filter then
  deletes it — the provider ends with **nothing** where it should have received
  the bundled copy. The server performs the same KB-over-bundle merge inside each
  single-KB pull, so the discarded candidate cannot be reconstructed client-side.
  That is why `fetchCandidates` returns responses keyed by KB.
- **The revision had to be recomputed.** `FilterForProvider` used to inherit
  `m.Revision`. With per-provider sets that makes `ComputeDiff.InSync` compare
  two different manifests under one string, and — worse — changing a binding
  would not change the revision, so no drift would ever be detected. The lock now
  records the revision of the manifest actually applied, which also fixed a
  pre-existing incoherence for providers that support only a subset of kinds.
- **Unbinding needs no removal logic.** An unbound KB's artifacts leave the
  provider's manifest, so `ComputeDiff` marks them `Removed` and `PruneManaged`
  deletes them. The prune only touches files listed in the lock, so user-owned
  files survive. This is why the projection was built as a filter rather than as
  a separate teardown path.
- **Entry shape and entry set are different questions.** A bare `/mcp`
  auto-routes only when the *server* mounts exactly one KB
  (`MultiKBServer.Handler`); with four mounted and one bound, the client still
  needs `?kb=`. `entriesForKBs` therefore takes both lists. Symmetrically,
  `removeMCPEntries` keeps receiving the **union** of every known KB: it derives
  the names this client may own from the list it is given, so a filtered list
  would orphan an unbound KB's entry permanently. Both are pinned by tests.
- **The default resolves against live discovery, not the cache.** A provider
  with no binding receives every KB the server currently mounts, read from
  `/health` rather than from `known_kbs` — `connect` runs before that cache is
  refreshed, and a dry run never refreshes it, so using the cache produced an
  empty entry set on both paths.
- **A nameless endpoint disables bindings rather than starving the client.**
  When the server does not identify its KBs by name, no binding can match. Every
  provider then receives everything, with a warning, exactly as before this
  change — a missing signal is not a reason to strip a client. The one exception
  is a provider explicitly bound to no KBs, where the declaration is unambiguous.
- **`--client` leaves the others completely untouched**, including their MCP
  entries and their lockfile entry, so a targeted sync cannot half-migrate a
  provider nobody asked about.
- **`ManagedFile.Source` is `omitempty` and "empty means unknown".** Every
  lockfile written before this change has none; treating that as "wrong
  provenance" would make `doctor` flag every existing machine on upgrade, and
  `ComputeDiff` still compares `ContentHash` only, so nothing re-materializes.

**Consequences.** Every client's revision changes once, on the first sync after
release, because it is now computed after filtering — that is a recomputation,
not drift, and it must be called out in the release notes. Behaviour is otherwise
unchanged for anyone who declares no binding. `statusManifestFn` became
`statusManifestsFn` (per provider); `materializeForProviders` takes a
per-provider manifest map, with `uniformManifests` for the callers that
legitimately have one.
