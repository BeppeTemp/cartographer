---
topic: sync-provisioning
---

# D171 — A cross-KB collision is an error, not an alphabetical tie-break

**Decision.** `provisioning.DetectCollisions` reports every `kind`+`name` claimed
by two or more distinct `kb:` sources, and `MergeArtifactsStrict` turns that into
a `*CollisionError` naming the kind, the name and the claiming KBs. The client
merges through the strict variant (`fetchMergedManifest`), so a collision stops
the sync before anything is materialized. `MergeArtifacts` keeps its tolerant
behaviour for `BuildManifest`. `cartographer client bind` warns when a new
binding creates a collision, and `doctor` gains a `kb-collisions` check.

**Rationale.**

- **The silent resolution was unstable, not just undocumented.**
  `MergeArtifacts` deduplicates on `kind+name` *without* the source, and
  `preferArtifact` picked the alphabetically first `kb:` source. Nothing recorded
  that a second copy existed. Worse, unmounting the winning KB makes the losing
  one appear: the content an agent reads changes with no artifact, no hash and no
  revision having changed in the KB anyone was looking at.
- **Error, not warning.** A warning about an artifact the agent then actually
  loads is worse than a failed sync, because at that point the wrong answer is
  silent. The failure names both KBs and the remedy, which is a rename.
- **KB-over-bundle stays.** It is deliberate — a KB may override a bundled skill
  — and unambiguous, because the bundle is single. Only KB↔KB became an error.
- **Two functions, not a flag.** `BuildManifest` merges one KB plus the bundle,
  where the case cannot arise; giving it a strictness parameter would add a
  branch nobody can reach. `MergeArtifactsStrict` is the client's entry point and
  the tolerant one keeps its callers.
- **`fetchMergedManifest` split into `fetchCandidates` + merge.** The per-KB
  responses have to survive the pull for the collision to be attributable at all,
  and the same unmerged candidates are what `client bind` and `doctor` filter per
  provider. This is also the seam the filtered projection needs.
- **Per provider, not globally.** Two colliding KBs bound to two different
  providers are not a conflict. `collisionsForProvider` narrows a collision to
  the KBs bound to one provider, so the warning does not train an operator to
  ignore it.
- **`client bind` stays offline.** The collision check needs the server, so it is
  best-effort: unreachable means "check skipped", not a failed command.
  Configuring a machine must not require the network, and the sync-time refusal
  is the backstop.

**Known limitation, since closed by [D172](D172-sync-ordering-per-provider-checkpoints-and-a-client.md).** As written here, `runSync`
rewrote the providers' MCP entries *before* fetching the manifest, so a refused
merge still left those rewritten; only artifacts and the lockfile were protected.
A test pinned that behaviour so the reorder could not land silently. D172
reordered `runSync` and inverted the assertion: a refused merge now leaves the
MCP entries untouched as well.

**Consequences.** A deployment with two colliding KBs — working today, silently
and arbitrarily — starts failing its sync with a report and a remedy. That is the
intent. Everything else is unchanged: single-KB clients and clients whose KBs
have distinct artifact names never reach the new code path.
