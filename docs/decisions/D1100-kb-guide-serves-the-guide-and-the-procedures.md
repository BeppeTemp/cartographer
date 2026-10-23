---
topic: control-plane
---

# D1100 — kb_guide serves the operating guide and the bundled procedures

**Decision.** One read-only, core tool, `kb_guide([procedure], [file])`, returns how to work in
the KB of the call: the operating guide (the text of D1097, from the same renderer, for this one
KB), a list of the served procedures and, for a principal with whole-KB read, the KB's own skills.
With `procedure` it returns the body of a bundled skill (`ingest`, `conflict-resolve`, `import`,
or the full `cartographer-kb-*` name) without its frontmatter, after a fixed preface and, for two
of them, a one-line note; with `file` too, one file of that procedure's `references/`. The
registry of served procedures lives in `internal/skillbundle/procedures.go`, the tool in
`internal/mcpserver/tools_guide.go`.

**Why.** A host with no Cartographer client side (a chat UI with native MCP, a gateway that
snapshots `tools/list`, a custom agent loop) gives the model only tools: nothing documents
forwarding `initialize.instructions` to the model, and the bodies of the bundled skills reach a
client only through provisioning, which needs a filesystem. `skill_list` lists names, never
bodies, and `skill_install`/`skill_list` are advanced, so a descriptor-bound host cannot call
them (D123). A tool is the one channel every such host honours. It costs about 0.65 KiB on the
`tools/list` of every connection, provisioned ones included; the 29 KiB budget is not raised.

**Alternatives rejected.**
- *An MCP prompt or resource*: those hosts list neither to the model.
- *Serving every bundled skill*: `cartographer-kb-create`, `cartographer-kb-doctor` and
  `cartographer-ops` run commands, scripts and subagents or configure the server's host; they
  are not served (a server-side health system is to replace the doctor, out of scope here).
- *Editing the bundled skills for tools-only readers*: they are shared with provisioned clients
  and a changed skill bumps its version. A fixed preface (a step needing a command, a script or a
  subagent is skipped; a tool not in the list is unavailable; "the operator" is the person you
  work with) and a note for `ingest` and `import` carry the difference instead.
- *Accepting the pre-D958 names (`kb-ingest`, …)*: the tool has no history to stay compatible
  with. The schema types `procedure` as a plain string, not an enum, so a full name stays valid
  and the descriptor stays small.
- *A whole-KB resource class*: the guide and the procedures are not KB content, so the tool
  needs only a read grant (collection class); a map-scoped principal gets them too. The KB's own
  skills are a listing of whole-KB files (`artifact_read` is whole-KB), so that section appears
  only for a principal with whole-KB read and is never hinted at for another.

**Consequences.** `kb_guide` is in `readOnlyToolNames` and in the agent list of
`TestServer_ToolsProfile`; it writes nothing, takes no lock and is not wrapped. It serves only
`bundled/<served skill>/SKILL.md` and files under that skill's `references/`, path-confined. A
new bundled skill fails `TestProcedures_EveryBundledSkillIsClassified` until it is declared served
or not, and a served body that runs a `cartographer <subcommand>` fails
`TestProcedures_CLIStepNeedsANote` until the entry carries a note. The managed block of a
provisioned client stays byte-identical: the one guide-side line naming `kb_guide` is not in it.
The advanced-tool mirror in `bundle_test.go` still lacks `secret_set` and `sync_check`, which two
served bodies cite; the preface and the `ingest` note cover them.
