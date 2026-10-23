# AGENTS.md — control plane

The MCP protocol and the tool registry. Every invariant below is enforced by a
test or a decision record: none of them is a preference.

## Adding a tool

1. `func toolName(k *kb.KB) Tool` in the `tools_<domain>.go` that owns the domain.
2. Handler: deserialize args → call `kb`/`okf` → `textResult`/`errorResult`.
3. Register it in `RegisterKBTools` (`tools.go`). A new tool is core or advanced: add it to the agent list of `TestServer_ToolsProfile` or to `advancedToolNames`, and give it a resource class in `policy.go` (`resourceClassForTool`), or registration panics.
4. Test in `server_test.go`.
5. `make gate` green.
6. Update `docs/control-plane.md` §MCP API, and add a decision file if the choice
   is architectural or a contract (D672) (`make decisions-new`; the release PR regenerates the index (D328)).

## What you must not get wrong

- **A text a connection can receive carries no `cartographer <command>` unless it
  is chosen by connection kind** (`addressesOperator`, D1101). Add the generic
  wording beside it; `TestGenericConnectionIsNeverToldToRunACLICommand` fails
  otherwise. Scenarios in `test/e2e/` grep these texts too, and `make gate`
  does not run them: a wording change runs `make e2e` before the push.
- **A tool's `ReadOnly` field and `readOnlyToolNames` are two halves of one fact.**
  `readonly.go` carries the source-of-truth list; `TestReadOnlyToolsGolden` builds
  a real registry and fails if they diverge. A new read-only tool that is not in
  the list is refused for a `kb:<name>:r` token; a mutating tool wrongly in it is
  a privilege escalation.
- **A handler never takes the lock, never commits, never syncs.** `gitwrap.go`
  wraps every write with the per-KB lock, the commit and the git sync. Doing any
  of it inside a handler double-commits or deadlocks. The only exceptions are
  `concept_transfer` (D760) and `artifact_move` (D782), which span two KBs and so own both
  locks and both commits, through the shared `twoKBMove` (`twokbmove.go`); a tool that needs
  that has to say why in its decision. `git_conflict_resolve` is the third: it takes the KB
  lock itself and finalizes merge, commit and push of the conflicts it settles
  (`kb.FinalizeConflicts`), because the sync that `gitWrap` runs first would hit the same
  conflict again. A client of `twoKBMove` must supply
  `TargetFiles`: `git add -A` skips a file an ignore rule matches without an error, and the
  skeleton checks the target commit's listing against it before the source is touched (#845).
- **Do not `os.WriteFile` a path you have not `Lstat`ed.** The KB side refuses to
  traverse a symlink for a reason: writing through one truncates the target
  instead of replacing the link, which is how a single sync modified 23 files in
  an unrelated checkout (D148).
- **A fact about the request (`unknown`, `renamed`, `provisioned`) rides the
  context, never the cached routed view.** The view is shared by every URL that
  resolves to the same set, and the target KB's server (where the doctor nudge
  runs) is not the view. For the same reason `initialize` instructions are set
  per handshake by `guideMiddleware`, not at build time: the SDK server is
  memoised (D1097).
- **Tool names are part of the public contract.** `TestDocsToolNamesExist` fails when a
  doc, a bundled skill, `.claude/` or an area `AGENTS.md` cites a tool that is not
  registered; `TestToolTableMatchesRegistry` and `TestToolTableParametersMatchSchemas`
  fail when a tool has no row in `docs/control-plane.md`, a marker or a parameter
  disagrees with the registry (D101, D1125). Renaming one is a breaking change,
  not a cleanup.
- Registration is per-KB: a tool added outside `RegisterKBTools` is invisible in
  multi-KB mounts.
- **A test that gives a KB a remote makes it sync with that remote** (D903: a KB syncs
  exactly when it has one, and no setting turns it off). Use a bare repository in
  `t.TempDir()`, or an address that refuses at once such as `127.0.0.1:1`; never a real
  host, which a read or a write would then contact.
