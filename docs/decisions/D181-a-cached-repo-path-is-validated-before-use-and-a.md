---
topic: sync-provisioning
---

# D181 — A cached repo path is validated before use, and a change of roots invalidates the cache

**Decision.** `repoindex.lookupIndex` no longer serves a cached candidate path
as-is: each candidate's paths are filtered down to the ones that currently hold
a live clone (a directory containing a `.git` entry, checked with the
symlink-following `stat`, one call per candidate), and ambiguity/emptiness is
evaluated on the survivors, not on the raw cache. `Resolve` also compares the
cache's `Index.Roots` against the `roots` argument (each element normalized
with `expandHome`, order-sensitive) before trusting the cache at all — a
difference skips `lookupIndex` entirely and falls through to a fresh `Scan`.

**Context.** `Resolve` served a cache hit with no filesystem check: moving a
clone left every `{{repo:<key>}}` citing it resolving to the old, now-wrong
location, silently and indefinitely — `service restart` did nothing, since the
stale value lives in `~/.config/cartographer/repos.json`, not in server memory.
Only a deliberate miss (resolving a nonexistent key) rewrote the cache, by
accident. Two adjacent gaps made it worse: `Index.Roots` was persisted and never
read, so editing `search_roots` didn't invalidate anything either; and with
multiple clones of the same remote, a dead first-in-root-order entry kept
winning over a live second one. Worst case, a leftover clone at the old
path (an old copy, a `.Trash` copy) made the wrong resolution look like a
correct one — an agent following the placeholder read, or committed into, the
wrong working tree with nothing to signal it.

- **Refresh-on-miss stays the only refresh path (D75).** No watcher, no daemon,
  no TTL: a stale hit now degrades to a miss, which was already the trigger for
  a rescan. The behaviour that repairs staleness is the one D75 already
  specified — it just never used to fire.
- **A hit stays cheap.** Validation costs one `stat` per candidate path
  (following a symlinked clone correctly), never a filesystem walk — the depth
  cap exists precisely to keep `Scan` off the common resolution path, and this
  does not reintroduce it.
- **Root order still decides the winner** among multiple live clones; the
  filter only removes candidates that are not really there anymore.
- **An ambiguous short name that is only ambiguous on paper — because one of
  the colliding remotes has no live clone left — now resolves instead of
  erroring.** That is the intended fix, not a side effect: the ambiguity was an
  artefact of a stale index, and D75's "ask for the full form" rule exists to
  disambiguate remotes that really do coexist on disk.
- **An empty-vs-nonempty `Roots` counts as a difference.** A cache written
  before this change carries no `Roots`, so the very first resolution after
  upgrade always rescans once — an intended, one-time cost, not a bug.

**Consequences.** No interface, configuration, CLI or MCP surface change — this
is a behavioural fix. The first resolution after upgrading to this version
rescans once per cache, per the `Roots` edge case above. `fix:` release.
