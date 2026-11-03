---
topic: sync-provisioning
---

# D1556 — Verification covers the files the lock owns; a foreign file is information

**Decision.** The on-disk re-hash of a skill or hook covers only the files the lockfile records for it (content and mode), the same set the materialized hash was computed over. A file the lock does not own inside the managed directory is a separate finding, `foreign`: it is not healable, it does not make `status` exit 1, and `doctor` lists it as information. This replaces the D139 line that an extra file in the directory counts as modified.

**Why.** The heal rewrites the artifact's own files and rightly leaves a foreign one, and the recorded hash covers only the artifact's files, so a whole-directory hash could never match again: every run "healed" the artifact, reported a discarded local modification, and `status` exited 1. A Finder `.DS_Store` or a `__pycache__` created by the skill's own script is enough, and the bootstrap hook runs `sync` at every session start.

**Alternatives rejected.**
- Keep the whole-directory hash and ignore a fixed list of OS junk (`.DS_Store`, `__pycache__/`): a list that is never complete, and the rule would still be wrong for any other file.
- Make the heal delete the foreign file: it is not ours to delete (D63, D178).

**Consequences.** The lock entries (one per file) are the ownership record; a lock that lists no file of the directory falls back to hashing the whole directory. `disconnect` treats `foreign` as safe to prune: it removes owned files and leaves the foreign one and its directory. A new consumer of `DriftFinding` must decide what `foreign` means for it.
