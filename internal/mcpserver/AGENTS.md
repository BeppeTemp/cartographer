# AGENTS.md — control plane

The MCP protocol and the tool registry. Every invariant below is enforced by a
test or a decision record: none of them is a preference.

## Adding a tool

1. `func toolName(k *kb.KB) Tool` in the `tools_<domain>.go` that owns the domain.
2. Handler: deserialize args → call `kb`/`okf` → `textResult`/`errorResult`.
3. Register it in `RegisterKBTools` (`tools.go`). A new tool is core or advanced: add it to the agent list of `TestServer_ToolsProfile` or to `advancedToolNames`, and give it a resource class in `policy.go` (`resourceClassForTool`), or registration panics.
4. Test in `server_test.go`.
5. `make gate` green. A new tool grows the cells of `TestFixedCallCostBudget` and must fit their budgets.
   A tool that reveals or changes a secret goes in `secretTools` (`policy.go`) and in `TestSecretsNoneTools`, or a `secrets: none` token slips past it.
6. Update `docs/control-plane.md` §MCP API, and add a decision file if the choice
   is architectural or a contract (D672) (`make decisions-new`; the release PR regenerates the index (D328)).

## What you must not get wrong

- **A text a connection can receive carries no `cartographer <command>` unless it
  is chosen by connection kind** (`addressesOperator`, D1101). Add the generic
  wording beside it; `TestGenericConnectionIsNeverToldToRunACLICommand` fails
  otherwise. Scenarios in `test/e2e/` grep these texts too, and `make gate`
  does not run them: a wording change runs `make e2e` before the push.
- **A path an artifact tool names has no hidden segment, and a tool that moves a whole folder
  checks what is inside it.** `classifyArtifactPath` refuses any segment that starts with a dot (a
  nested `.git` is no part of an artifact); `artifact_move` and `moveLegacySkill` carry a folder
  whole, so they walk it with `hiddenEntryUnder` first. The commit side holds the same line: `gitx.Commit` and
  `CommitPaths` refuse a gitlink (`gitx.ErrGitlink`). A new tool that writes under `skills/` or
  `hooks/` goes through the guard; one that renames or copies a directory checks its contents.
- **A tool's `ReadOnly` field and `readOnlyToolNames` are two halves of one fact.**
  `client.ReadOnlyToolNames` (`internal/client/readonly.go`, where a tool author adds the name) is the source-of-truth list; `TestReadOnlyToolsGolden` builds
  a real registry and fails if they diverge. A new read-only tool that is not in
  the list is refused for a `kb:<name>:r` token; a mutating tool wrongly in it is
  a privilege escalation.
- **The `?access=read-min` listing (`readMinTools`, `readprofile.go`, D1184) stays inside
  `readOnlyToolNames`**: `TestReadMinToolsAreReadOnly` enforces it, and a write is refused
  at the routed authorizer with `read_only_profile`, never left to the target KB.
- **A JSON tool result is built with `jsonResult` (`protocol.go`), compact.** `MarshalIndent` in
  a non-test file is refused by `TestNoIndentedMarshalInToolResults`, and `TestToolResultGolden`
  proves a result is its `testdata/results` golden up to whitespace (D1185).
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
- **A fact about the request (`unknown`, `renamed`, `provisioned`, `readonly`) rides the
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
- **`kb_guide {}` stays the whole guide**, whatever `initialize` carries (D1100, D1316): it
  is the only route for a host that drops `initialize.instructions`. Tell the agent it
  already holds the text; never empty the fallback.
- **A request is one unit of work**: a read handler that runs several passes (search's
  glossary variants and archived fallback) shares one `searchPass` — one reconcile, one
  ranking view, one archived set. A helper that validates the graph or asks
  `ConceptFacets` per pass or per candidate shows in `search_pass_test.go`, which counts
  `k.GraphValidations()` and `k.GraphFacetLookups()`.
- **Anything read out of git for a caller is filtered by `historyGuard`
  (`history_visibility.go`).** A revision list, a file at a past `rev`, a commit
  subject or reason: the policy gate checked only the page's current id, but
  history follows renames and a commit touches several files. A new tool or hint
  that returns commit data builds `newHistoryGuard(ctx, k)` and uses
  `revisions`, `pathVisible` and `redact`; a nil guard is a whole-KB caller.
- **An error that can hold a filesystem path goes through `relPathErr(err, k.Root)`** before
  it enters a tool result (`errpaths.go`): an `*fs.PathError` or `*os.LinkError` printed
  with `%v` shows the directory the server keeps its data in, which a client of a shared
  server has no use for. `twoKBMove.run` scrubs both roots from its error results.
