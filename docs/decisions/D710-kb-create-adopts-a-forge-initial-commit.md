---
topic: deployment-release
---

# D710 — `kb create --remote` adopts a forge's initial commit

**Status: implemented (2026-10-12).** Amends [D134](D134-kb-create-requires-a-git-remote.md) (`kb create` requires a remote) and [D156](D156-service-path-restart-after-stop-and-a-kb-create-that.md) (a failed push keeps the scaffold); relies on [D264](D264-kb-writes-follow-the-remote-default-branch.md) and [D265](D265-the-author-is-the-committer-when-git-has-none.md). Closes #710.

**Context.** A forge that creates a project with "initialize with README" leaves one commit on its default branch (`main` or `master`). `kb create --remote` then failed at the push (or, on `master`, pushed a second branch that D264 treats as divergence) and printed an authentication hint and a `kb clone` hint, both wrong.

**Decision.**

- **Automatic adoption, no flag.** Before pushing, `kb create` asks the remote (`ls-remote`). Empty: today's push. Forge-initialized: the scaffold commit is rebased onto the forge commit, the forge's files are kept, and the push is a fast-forward, never forced.
- **Strict classification.** One advertised branch, one commit, every tree path top-level and a README/LICENSE/LICENCE/COPYING (optional extension), `.gitignore` or `.gitattributes` file (`gitx.ForgeInitialized`). Anything else "holds content".
- **The branch follows the remote's default** (its single branch when HEAD is unadvertised), whatever its name: a forge `master` never gets a `main` beside it (D264).
- **Identity.** The rebased commit keeps the resolved identity as author and committer (D265).
- **A path collision is a refusal**, never a merge, naming the paths and how to empty the repository.
- **A pre-push refusal deletes the local KB** (collision, remote holding content, failed fetch or rebase): nothing was pushed and the next step is a rerun, which an existing directory would block. A push failure still keeps the scaffold (D156); an unreachable remote takes that path unchanged.
- **Other non-empty remotes are refused** with the `kb clone` and kb-import pointers. `setup`'s routing is unchanged; `kb clone` of a forge-initialized repository points at `kb create --remote`.

**Alternatives rejected.** Force-pushing a replacement discards remote content and protected default branches reject it. A `--adopt` flag adds a rerun with nothing for the user to decide, as the classification only matches a commit the forge wrote at project creation.
