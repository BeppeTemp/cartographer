---
topic: sync-provisioning
---

# D2018 — A tracked instructions file carries none of this machine's paths

**Decision.** A project-scope instructions block whose destination file git tracks — the
file itself, or the file inside the workspace a symlink at the destination (in the file or in a
directory above it) resolves to — carries the placeholder paragraph
([D262](D262-path-placeholders-are-visible-resolved-and-onboarded.md)) and none of this machine's
absolute paths: the "Local paths" table has no rows, and the `{{repo:<key>}}`/`{{path:<name>}}`
placeholders in the KB's own instruction text stay as written (an escaped `{{\repo:…}}` still loses
its backslash, metasyntax stays verbatim), so the agent resolves them with `cartographer resolve`,
which the paragraph names. A destination that resolves outside the workspace (a subdirectory's
`CLAUDE.md -> ../AGENTS.md`) or cannot be resolved (a dangling or looping link) counts as tracked
without running git, and so does one that lands inside a nested repository or a submodule (a `.git`
between the workspace and the file): the workspace's tracked set says nothing about the file it lands in.
The file is asked about whatever its ASCII capitalisation (`:(literal,icase)` per pathspec, directories
included): a case-insensitive filesystem writes `CLAUDE.md` into the `claude.md` a repository tracks,
and on a case-sensitive one the answer errs toward withholding. git folds ASCII letters only, so a
non-ASCII directory that differs from the tracked one only by case is a stated limit. The
client decides it once per projection (`provisioning.InstructionsDestTracked`, handed to `Apply`
and to the status diff as `ApplyOptions.WithholdLocalPaths`), so the paths-section hash that sync
records is the one status recomputes. An untracked or client-owned destination (`CLAUDE.local.md`,
a kiro steering file, an untracked `AGENTS.md`) and the global scope keep their paths; a workspace
in no repository keeps them too, as before. Where git is asked and fails (dubious ownership, git missing,
an unreadable index) they are withheld: the file may be shared, and writing a machine's paths into a
shared file is the unsafe side. Skills,
agents and the other kinds are written to their own files and are expanded as usual.

**Why.** The paths are absolute paths of one machine, usually containing the OS user name
([D259](D259-published-text-carries-no-private-names.md): a real setup does not belong in a shared
file). An explicit `workspace bind` ([D193](D193-workspace-scoped-artifact-projection-one-provider.md))
writes the block into the project's `CLAUDE.md`/`AGENTS.md`, which a repository often versions,
and through a `CLAUDE.md -> AGENTS.md` link into the tracked target: every developer who syncs
rewrites a committed file with their own paths, and the diff never settles (#1897). The table is one
way they get in; the KB's own instruction text is the other, since its placeholders were expanded
into the block. The paragraph is the same text for everyone and already tells the agent to run
`cartographer resolve`, so the agent loses a cache, not a capability. What it costs: in a tracked
file the agent resolves each key on demand, and a file that becomes tracked after a first sync is
rewritten once, without the paths.

**Alternatives rejected.**
- *Leave the table and warn.* The paths are on the remote as soon as someone commits; a warning
  after the write is too late.
- *Write the block to a local file for every client.* Codex and Copilot have no untracked
  instructions file the client reads beside the tracked one; a different file per client is a
  separate decision about where an explicit bind writes, not this one.
- *Withhold the paths everywhere.* An untracked or client-owned file is the one place the machine's
  paths are safe and useful.
- *Judge the file in `sync` only.* `status` rebuilds the hash without the sync's inputs; a flag only
  one side knew reported drift forever (the `TestShared_Convergence` failure mode).
- *List the whole index to answer one question.* The client asks git about the one or two paths
  concerned (`gitx.TrackedPathsFold`, literal case-folded pathspecs), with the variables that pin a repository
  (`GIT_DIR`, `GIT_INDEX_FILE`, …) and those that change how a pathspec is read (`GIT_LITERAL_PATHSPECS`, …) dropped, since a sync run from a git hook inherits them and would
  answer for the repository running the hook; the other client git reads that name a workspace
  with `-C` (the owned-file listing that feeds sync, the bind guard's `origin`) run the same way.
- *Ask git about the name on disk.* A file tracked but absent from the disk, or a directory that
  differs only in case, has no on-disk spelling to ask about; folding the question needs no
  filesystem.

**Consequences.** Not decided here: whether an explicit bind should write into a tracked file at
all, or offer a bind without the block ([D193](D193-workspace-scoped-artifact-projection-one-provider.md)
stands; a derived binding still never edits a tracked file, [D1019](D1019-a-repository-a-kb-declares-is-bound-to-it.md)).
A new client-written part of the block that depends on the machine needs the same answer for a
tracked file. `doctor`'s symlink finding names the tracked target and offers replacing the link
before listing it, and says so when the link itself is tracked, since replacing it then changes
the repository.
