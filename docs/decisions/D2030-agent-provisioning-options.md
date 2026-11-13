---
topic: sync-provisioning
---

# D2030 — an agent says which clients it is for, where its prompt lives, and a binding can reach its linked worktrees

**Decision.** Four additive options, none of which changes the lockfile shape. (1) An agent's frontmatter
`clients:` is an allow-list (default every client): it is read by the one predicate `contract.AgentSkipped`,
beside `strict_tools`, so sync, status and the shadow check of a tracked agent agree. (2)
`providers.<client>.prompt_file` is a directive, never a native field: a client that can reference a file
(`contract.PromptFileClients`, Kiro) gets a reference to a file of the bound workspace as its prompt, any
other uses the body; it is read only through `resolvePromptFile` and refused (the agent held for that
client) when absolute, escaping the workspace or on a global binding. (3) `workspace bind --worktrees` is an
opt-in stored on the explicit binding (`worktrees: true`, absent means off). (4) With it, `sync` lists
`git worktree list --porcelain -z` of the bound folder and projects the same workspace artifacts into each
other working tree (the main checkout too when the bound folder is a linked one), each under its own lock key,
untracked and excluded like the bound folder; a worktree that disappeared has its record dropped silently, but only
a record marked `worktree: true` in its lock entry: any other undeclared workspace with a missing directory takes
the ordinary, reported prune.

**Why.** An agent written for one client was projected on all of them, a Kiro prompt could not live in a
file the repository versions, and a session in `<repo>/.worktrees/<slug>` saw none of the projected files
because a worktree is another folder. Each option is the smallest declaration that fixes one of these.

**Alternatives rejected.** A per-client agent directory in the KB: duplicates the body. Copying
`prompt_file` as a native field: the path is relative to the workspace, not to the client's directory.
Worktree projection by default or for derived bindings: it writes into folders the user never named, and a
derived binding has no flag. Binding each worktree by hand (still possible and wins): it does not scale to
short-lived worktrees. Pruning a vanished worktree with the usual orphan path: its `PruneManaged` and
exclude rewrite fail on a missing directory and the line is noise.

**Consequences.** A new place that skips an agent per client must call `AgentSkipped`. A path read from a
KB is joined to a workspace only through `resolvePromptFile`. A failed worktree listing (any git failure but "not a repository") is an
error of `gitx.LinkedWorktrees`, never an empty list: it never fails the main projection and suspends the orphan
prune of that provider's existing undeclared workspaces for the run (the vanished-directory drop of a marked
worktree still happens). `prompt_file`'s link check is point-in-time: Kiro resolves the reference later (accepted
residual, documented in `docs/sync.md`). A worktree outside the bound folder is not covered by MCP tool
scoping (`ResolveWorkspace` matches by path prefix). Older clients ignore the three frontmatter and binding
keys and keep projecting every agent everywhere with its body.
