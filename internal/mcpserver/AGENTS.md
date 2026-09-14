# AGENTS.md — control plane

The MCP protocol and the tool registry. Every invariant below is enforced by a
test or a decision record: none of them is a preference.

## Adding a tool

1. `func toolName(k *kb.KB) Tool` in the `tools_<domain>.go` that owns the domain.
2. Handler: deserialize args → call `kb`/`okf` → `textResult`/`errorResult`.
3. Register it in `RegisterKBTools` (`tools.go`).
4. Test in `server_test.go`.
5. `make gate` green.
6. Update `docs/control-plane.md` §API MCP, and add a decision file if the choice
   is non-obvious (`make decisions-new`, then `make decisions-index`).

## What you must not get wrong

- **A tool's `ReadOnly` field and `readOnlyToolNames` are two halves of one fact.**
  `readonly.go` carries the source-of-truth list; `TestReadOnlyToolsGolden` builds
  a real registry and fails if they diverge. A new read-only tool that is not in
  the list is refused for a `kb:<name>:r` token; a mutating tool wrongly in it is
  a privilege escalation.
- **A handler never takes the lock, never commits, never syncs.** `gitwrap.go`
  wraps every write with the per-KB lock, the commit and the git sync. Doing any
  of it inside a handler double-commits or deadlocks.
- **Do not `os.WriteFile` a path you have not `Lstat`ed.** The KB side refuses to
  traverse a symlink for a reason: writing through one truncates the target
  instead of replacing the link, which is how a single sync modified 23 files in
  an unrelated checkout (D148).
- **Tool names are part of the public contract** and are CI-enforced against the
  documentation (D101). Renaming one is a breaking change, not a cleanup.
- Registration is per-KB: a tool added outside `RegisterKBTools` is invisible in
  multi-KB mounts.
