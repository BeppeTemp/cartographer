---
topic: deployment-release
---

# D177 — `kb rename` is offline, bounded, and says what it does not migrate

**Decision.** `cartographer kb rename <old> <new>` renames a KB's mount point —
the directory and the matching `kbs[]` entry, together or neither — after a
preflight that reports every other reference to the name it can find. It never
contacts a server, a client or a git remote.

**Context.** Renaming a KB meant moving the directory by hand and editing
`server.yaml`, with nothing checking that the two stayed consistent and no way to
see what else depended on the old name. A KB's name is an identity: the HTTP
endpoint (`/mcp?kb=`, `/mcp/<name>`), the derived tool prefix, auth scopes
`kb:<name>:r|rw`, and the client-side `signing_keys` / `mcp_approvals` keys.

- **Bounded scope, honest rollback.** Directory plus server config, both local,
  is the only pair this command can undo. A command that promises to fix
  everything is one that will half-fix something, so everything else is reported
  instead: scopes, role rules and client-side pins are configured out of band and
  are not rewritten here or by a later sync. Silently editing an operator's auth
  configuration would be the worse failure.
- **Offline by construction.** The git `origin` is untouched — renaming a local
  mount point is not renaming a repository — and clients need no orchestration:
  `removeMCPEntries` + `applyMCPEntries` already perform the rename on the next
  sync, so driving them from here would duplicate a mechanism that works.
- **The directory moves first.** It is the step that can fail for reasons outside
  the process (permissions, a different filesystem). A failed config write renames
  it back; that is the only rollback claimed, and it is implemented.
- **A cross-device rename fails.** Falling back to a recursive copy would silently
  change the ownership and timestamps of a git repository. The operator is told to
  move it themselves.
- **Ambiguity refuses, absence does not.** Two `kbs[]` entries that could both be
  the KB is a refusal naming them, because guessing detaches a KB from its
  configuration. **No** entry is not: a KB created by `kb create` or found by
  discovery legitimately has none (D151), and refusing there would make the
  command useless in its most common case — the directory rename is then the whole
  job, and the output says so.
- **A derived prefix is announced, not prevented.** With
  `mcp.tool_prefix_mode: kb-name` the rename renames every tool the agents see.
  That is a legitimate consequence of renaming a KB; the failure mode to avoid is
  discovering it afterwards, so both prefixes are printed first. An explicit
  `kbs[].tool_prefix` is preserved verbatim.
- **The preflight writes nothing**, including no `kb.Open` — which self-migrates a
  repository's git-exclude entry. Validity is read from `data/index.md` directly,
  the same file `Open` checks.
- **The config is edited as a YAML node tree**, so comments, key order and
  unmodelled fields survive: a rename must not reformat a hand-written server
  config.
- **No restart.** `--restart` is offered, mirroring `kb create`/`kb clone`, and a
  failed restart does not roll the rename back: the files are already consistent,
  and undoing them because a supervisor misbehaved would leave a worse state.

**Consequences.** A new subcommand; nothing existing changes behaviour. Minor.
