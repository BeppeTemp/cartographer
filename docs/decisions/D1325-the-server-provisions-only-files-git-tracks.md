---
topic: client
---

# D1325 — The server builds KB artifacts only from files git tracks

**Decision.** A KB's skill, agent, hook and mcp artifacts are built from the files `git ls-files` lists in the KB clone, never from the working tree. An untracked or git-ignored file in an artifact directory is neither shipped nor hashed. A KB that is not a git clone (no `.git`) is not filtered. When git cannot answer for a KB, that KB's artifacts are withheld and the reason is reported in the manifest `issues`; the other KBs are unaffected.

**Why.** A `__pycache__` left by running a skill's script, though git-ignored, reached clients and changed the revision (#1325). The tracked set is what the KB's history says the artifact is. Failing closed per KB costs that KB's sync during a git fault, but falling back to the working tree would leak exactly the files this rule excludes. The listing is cached per KB root, revalidated by the git index's mtime and size, so one build runs git once and the manifest and the served files use the same set; hashes of tracked files are unchanged, so an unchanged KB keeps its revision and signatures.

**Alternatives rejected.** Filtering with `.gitignore` rules alone: it misses untracked non-ignored files and a tracked file would be dropped. Failing the whole manifest on a git error: one broken KB would stop every other KB (and the startup scan). Falling back to the working tree on error: it reopens the leak.

**Consequences.** A file written outside a tool and not committed is not provisioned until it is committed; tool writes commit on their own. A skill whose `SKILL.md` is untracked is not an artifact. Tests that write fixtures straight into a KB tree commit them (`commitFixtures`). Current behaviour: `docs/sync.md` §Manifest, `docs/control-plane.md` (`sync_pull`).
