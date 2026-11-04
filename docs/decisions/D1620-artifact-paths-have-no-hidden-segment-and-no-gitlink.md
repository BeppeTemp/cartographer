---
topic: concurrency-git
---

# D1620 — artifact paths have no hidden segment, and a KB never records a gitlink

**Decision.** A KB repository holds files, never other repositories. The artifact tools (`artifact_read`, `artifact_write`, `artifact_delete`, `artifact_move`) refuse a path with a hidden segment: a name that starts with a dot, or the NTFS short name of `.git` (`GIT~<n>`), with `/` and `\` both treated as separators. `artifact_move` also refuses a directory with a hidden entry below it, a junk file such as `.DS_Store` excepted. On the git side, every server git command ignores submodules and a nested repository's fsmonitor, staging never records a change to a gitlink, and a commit that would record one fails with `ErrGitlink`, naming the paths.

**Why.** A nested `.git` is no part of an artifact, and once it is in the tree, git treats that directory as a repository of its own, with its own config. Refusing the name at the tool boundary is cheap and host-independent. Refusing the gitlink at commit time covers what the tools do not see: a tree written by hand or pulled from a remote. The cost: an artifact can no longer ship a dotfile (`.editorconfig`, `.gitignore`), and a KB that already had one has to rename it.

**Alternatives rejected.**
- Refuse only `.git` and its spellings: every file system and git version adds spellings, and a list of them is never complete. Refusing every dot segment is a rule that does not need updating.
- Check only at commit time: the file would already be on disk, inside the server's tree, before anything refused it.
- Accept the nested repository and only configure git to ignore it: the history would still carry a gitlink that every clone and every other git client interprets.

**Consequences.** A new tool that writes paths under a KB must reject hidden segments with the same predicate (`hiddenPathSegment`), and a new git call must go through `gitEnv`, so that it inherits the submodule and fsmonitor settings. A gitlink the history already holds is left out of `git add -A`, and removing it is the operator's job: the error tells them to delete the `.git` directory inside it. Current behaviour: `docs/data-plane.md`, `docs/concurrency.md` §How changes land.
