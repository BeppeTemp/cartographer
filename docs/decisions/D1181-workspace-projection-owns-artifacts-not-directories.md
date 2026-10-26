---
topic: sync-provisioning
---

# D1181 — A workspace projection owns its artifacts, not the whole directory

**Decision.** In a directory destination (skills, agents, hooks) a workspace projection owns
`<dir>/<name><suffix>` for each artifact it writes in the run plus each path the lockfile records
for it, not the directory. The collision check and the `info/exclude` block use that set; shared-file
destinations keep the rule they had. A derived projection (D1019) never edits a tracked file: a
tracked MCP or registration file is not written and `status` reports it, and any other collision
skips that projection with a warning while the run goes on; an explicit binding keeps the refusal.

**Why.** Derived bindings (D1019) put the check on repositories nobody bound, and a repository that
versions its own skills in `.agents/skills/` was refused as a collision on files Cartographer would
never write, which stopped every `sync` for every provider and KB. Excluding the directory also hid
the repository's new own skills from `git status`.

**Alternatives rejected.** A per-repository opt-out: it only hides the problem, and leaves the next
repository with its own skills refused. Aborting the sync on a derived projection's clash: one
repository nobody bound on this machine would stop every other.

**Consequences.** A KB artifact with the name of a tracked one is still refused, naming it, and is
never overwritten. The exclude block is rewritten each run, so a pruned artifact's line goes with it.
A client-generated hook is owned by its reserved name, so the first run excludes it before the lock
records it.
The owned set is written into `.git/info/exclude` before Apply validates names, so an invalid
artifact name fails the projection up front with Apply's own message, and a lock path outside the
provider's destinations is dropped.
