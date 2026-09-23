---
topic: sync-provisioning
---

# D236 — A Codex hook registration left in config.toml is drift

**Decision.** On-disk verification (D139) reports a Codex hook whose files are
intact but whose registration is still in `.codex/config.toml` as `unregistered`.
Because that finding can be healed, the next `sync` rewrites the hook, and
`registerCodexHook` migrates the registration to `hooks.json` (D230). When the
migration deletes the old `# cartographer:hook:<name>` block, it first moves out
every table inside it that is not one of the hook's `[[hooks.<Event>]]`
registrations, as D126 does for the MCP block.

**Why.** The migration used to run only when a hook was materialized, and a hook is
materialized only when its content changes. After an upgrade, a KB hook whose
content was unchanged kept firing twice, once from each file. `doctor` suggested
`cartographer sync` as the fix, but sync never touched the hook, so the fix did
nothing (#338). The block was also deleted whole, which destroyed a `[notice]`
table that Codex's own rewrite of the file had placed inside it. Treating the
stale registration as drift reuses the existing path: `status` counts the hook as
divergent, `sync --no-heal` reports it, and `sync` repairs it. The cost is one
extra read of `config.toml` per Codex hook on every verification.

**Alternatives rejected.**
- Migrate unconditionally at the start of every `Apply`: this adds a second place
  that writes `config.toml`, and `status` would still call the client in-sync while
  the hook fires twice.
- Have `doctor` name `reconnect` instead of `sync`: that fixes the advice, not the
  upgrade, and every user would still have to run a command they never needed
  before.
- Parse the block and rewrite only our tables: D58 forbids re-serializing the
  user's TOML. Cutting whole table groups out verbatim, as D126 does, keeps every
  byte.

**Consequences.** Only Codex hooks are checked for a stale registration; Claude has
a single settings file and so no second place a registration can linger. The
bootstrap hook is not in the manifest, so the heal pass cannot rewrite it, but
`EnsureBootstrapHook` registers it again on every run. Inside a hook block, a
`[[hooks.<Event>]]` table is assumed to be ours, because that is all D58 ever wrote
there.
