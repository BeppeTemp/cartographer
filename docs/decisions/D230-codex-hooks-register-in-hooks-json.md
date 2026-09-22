---
topic: sync-provisioning
---

# D230 — Codex hooks register in hooks.json

**Decision.** A hook materialized for Codex is registered in
`<base>/.codex/hooks.json` — the user layer and a trusted project's `.codex/`
alike — patched in place by its `.codex/hooks/<name>/` ownership marker, with
every foreign entry preserved. It replaces the hook part of D58, which wrote a
marker-delimited `[[hooks.<Event>]]` block into `config.toml`. A registration
an older client left in `config.toml` (the block and any marker-less copy of
D99) is migrated out on the next provisioning run, and removed on prune.

**Why.** Codex reads hooks from both files, but a layer holding both is merged
with a warning at every start (#327). `hooks.json` is where third-party
integrations register — Herdr's Codex integration writes its `SessionStart`
hook there, and recreates the file on every reinstall — so staying in
`config.toml` meant the warning for anyone with another integration. It is
also the better file to own a slice of: it holds hooks and nothing else, while
`config.toml` is rewritten by Codex itself (`[projects.*]`, `[notice]`,
`[hooks.state]`), which is what the whole marker/orphan/eviction machinery of
D99, D126 and D127 existed to survive. Its shape is Claude Code's
`settings.json` hooks, which the client already patches by ownership marker.

**Alternatives rejected.** Keeping `config.toml` and moving other tools' hooks
into it: they recreate `hooks.json` on update, so the warning always comes
back. Writing both files: that is the double representation Codex warns about,
and a hook registered twice fires twice. Leaving old registrations for the
user to clean up: a stale block fires alongside the new entry.

**Consequences.** Codex keys hook trust (`[hooks.state]` in `config.toml`) by
`<file>:<event>:<i>:<j>`, so a migrated hook is prompted for trust once; the
migration says so in its warning. The trust tables themselves are left alone —
Codex's bookkeeping, inert once the registration they hash is gone. An inline
command gains the same trailing `# cartographer-hook:` marker comment Claude's
does, so it stays idempotent and prunable. A `hooks.json` left as an empty
object after prune is removed rather than kept as `{}`; one holding anything
else stays. `doctor` counts a registration still in `config.toml` as a stray.
The `config.toml` MCP block is unchanged.
