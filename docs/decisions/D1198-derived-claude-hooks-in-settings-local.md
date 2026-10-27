---
topic: sync-provisioning
---

# D1198 — A derived binding registers Claude Code hooks in settings.local.json

**Decision.** A derived workspace binding (D1019) registers its Claude Code hooks in
`.claude/settings.local.json`, the settings layer Claude Code reads beside `.claude/settings.json`
and that a repository does not version, and excludes it from git like `CLAUDE.local.md`. It never
writes `.claude/settings.json`. When the repository tracks `settings.local.json` too, the hooks are
not projected (D1181's rule for a tracked registration file). Wherever a Claude Code settings file
is still rewritten, its key order, indent and final newline are kept.

**Why.** A derived binding writes into repositories with no command run there, so it may not edit a
tracked file (D1019). A team's tracked `settings.json` showed up modified in `git status` with the
KB's hooks in it, its keys reordered and its final newline gone. Registering the hooks in an
untracked file keeps them working, the same answer `CLAUDE.local.md` gives for the instructions.

**Alternatives rejected.** Skipping hooks whenever `settings.json` is tracked: most Claude Code
repositories track it, so derived bindings would lose their hooks almost everywhere. Moving the
registration only when `settings.json` is tracked, like `CLAUDE.local.md`: a repository that starts
tracking the file later would commit Cartographer's entries with it. Stripping the entries an
earlier version left in a tracked `settings.json` during `sync`: that edits a tracked file too.
`doctor` reports them and the user removes them.

**Consequences.** A hook already registered in `settings.json` is not registered again in
`settings.local.json`, which would make it fire twice. Once the entry is removed, the next sync
registers the hook in `settings.local.json`: Apply rewrites a derived Claude Code hook that is
registered in neither file. A prune that does not know which file holds a registration
(`disconnect`, an orphan projection) strips the hook from both files, and only rewrites a file that
names it. The registration readers (`doctor`, on-disk verification) read both files.
