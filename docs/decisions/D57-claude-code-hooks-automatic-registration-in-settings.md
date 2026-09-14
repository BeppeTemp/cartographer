---
topic: sync-provisioning
---

# D57 — Claude Code hooks: automatic registration in `settings.json`

**Context.** D48 materialized a hook (`hooks/<nome>/` → `.claude/hooks/<nome>/`) but stopped
there: `settings.json` was never touched, for fear of duplicating entries in
`hooks.<Event>[]` at every sync (nested arrays, no ownership marker) and of not being able to
prune safely — a materialized but dead hook, to be registered by hand.

**Decision.** For the `claude` provider only, `Apply` now automatically registers/updates the
entry in `<targetDir>/.claude/settings.json` right after materializing the hook's files
(`internal/provisioning/hooksettings.go`). Ownership criterion: an entry is "Cartographer's for
hook `<nome>`" if and only if its `command` contains the substring `.claude/hooks/<nome>/`
(`hookOwnershipMarker`) — the materialized path *is* the signature, no extra field to invent.
`upsertHookEntry` removes every entry with that marker and inserts a fresh one → idempotent. The
file is decoded into a `map[string]interface{}`, not a fixed struct: unknown keys
survive, only the key order is not preserved (acceptable: the invariant is "no user data
lost"). Prune removes the entry via the same marker when the hook disappears.
**Rationale.** A generic JSON deep-merge has no "obvious" criterion for an array without an
identifying key; the per-path marker is targeted and does not require extending the entry format (discarded: a
dedicated `_cartographer` field, risk of breakage with future Claude Code schema checks).
Details: `docs/sync.md` §Agents and hooks.

**Update (bare-command bug).** `resolveHookCommand` joined *any* non-absolute first token
(and non-`$VAR`) to the hook's dir — correct for `./notify.sh`, broken for a shell one-liner
like `jq -e ...`: it produced `.claude/hooks/<nome>/jq`, a nonexistent binary, and the command's
`|| true` masked the failure at every invocation. Now the join happens only if the token
contains `/` (relative path); bare names stay verbatim, resolved via PATH as in a shell.
Since the ownership marker *is* the path in the command, a bare command would never contain it
(entry neither idempotent nor prunable): for the claude provider only, `registerHookSettings` appends
the marker as an inert shell comment (`# cartographer-hook: .claude/hooks/<nome>/`) —
syntactically neutral, preserves D57's substring criterion without extra fields. Codex/OpenCode
do not need it (per-block/per-file ownership).
