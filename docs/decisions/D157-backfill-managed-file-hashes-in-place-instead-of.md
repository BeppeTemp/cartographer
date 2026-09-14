---
topic: client-configurator
---

# D157 — Backfill managed-file hashes in place instead of rebuilding every client

**Status: implemented (2026-08-28).** Amends [D138](D138-provenance-stamp-on-materialized-skills-and-agents-and.md) and
[D139](D139-on-disk-verification-sync-restores-what-diverged.md). Closes #178.

**Context.** `doctor`'s only prescribed remedy for a narrow metadata gap was a full client
rebuild:

```
info [managed-files] 6 managed artifact(s) recorded before content hashes existed
     cannot be verified — …/.cartographer-sync.lock.json
     fix: cartographer reconnect
```

Its reasoning was correct on every point: an entry with no materialized hash is `DriftUnknown`,
deliberately not healable — healing it would mean overwriting whatever is on disk on the strength
of no evidence — and `ComputeDiff` sees no change either, so `sync` leaves it alone.

The gap was the leap from *"cannot be compared"* to *"must be rebuilt"*. `reconnect` prunes and
rewrites **every** managed artifact on **every** client: in the field 28 and 29 skill trees, both
steering files, the hook directory and the MCP entries — roughly 150 file operations to backfill
**six** hashes. It worked and it was recoverable, but the blast radius was three orders of
magnitude larger than the problem, and a partial failure would have left both clients without
skills. There was no narrower option: `reconnect` takes only `-agents` and `-dry-run`.

**The content is already on disk and the hash is computable without refetching anything.** That is
the whole fix.

**Decision.**

- **Backfilling is not healing.** It records what is on disk *as* the baseline; it does not claim
  the file matches the server. A backfilled entry carries `adopted_at`, so a later genuine drift is
  still detectable from the next server-side change onward, and an operator can tell a verified
  entry from an adopted one. The command says so in as many words.
- **It is an explicit command, `cartographer doctor --repair-hashes`, not implicit in `sync`.**
  Doing it silently inside a sync would make an unverifiable entry become verified with no record
  of the decision — exactly the ambiguity `DriftUnknown` exists to prevent.
- **Only entries whose file exists are backfilled.** A missing file is already `DriftMissing`, a
  real finding for `sync` to fix, and hashing nothing would paper it over. A symlinked destination
  is skipped too, deferring to [D148](D148-provisioning-refuses-a-symlinked-destination.md): Cartographer does not hash a file
  it refuses to own.
- **The hash is computed exactly the way `Apply` records `materializedHash`** — same
  `hashArtifactFiles`, same ordered set of files, including the executable bit. A value computed any
  other way would never match a future `Apply`, turning an unverifiable entry into a permanently
  drifted one, which is worse than the gap.
- **`reconnect` is unchanged.** It remains the right tool for a genuine rebuild; this adds a
  narrower one beside it and changes `doctor`'s prescription for this one finding.
- **No new file format**: the lockfile is already v2 with a per-entry hash, so backfilling writes
  the same field plus one optional timestamp.

**Consequences.** Additive: one new flag, one changed `doctor` message, one new optional lockfile
field that older clients must ignore. The round trip — finding → `--repair-hashes` → finding
gone — is covered by a single test, so the loop cannot regress.
