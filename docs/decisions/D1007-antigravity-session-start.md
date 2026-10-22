---
topic: sync-provisioning
---

# D1007 — Antigravity receives SessionStart native hook and session bootstrap parity

**Decision.** Antigravity's hook registration mechanism in `internal/provisioning/hooksettings.go`
registers `SessionStart` hooks, and `hookEventReach` declares that `SessionStart` reaches
`configurator.ProviderAntigravity`. `noSessionStartEvent` is removed from Antigravity's
`hookMechanism` in `internal/provisioning/bootstrap.go`, allowing `EnsureBootstrapHook` to
install and manage the synthetic `cartographer-bootstrap` hook for Antigravity.

This amends D194 §Trigger semantics, which previously marked Antigravity with `noSessionStartEvent: true`
when Antigravity only exposed per-tool and per-invocation events.

**Why.** Recent versions of Google Antigravity (`agy` and Antigravity 2.0) support the
`SessionStart` event in `hooks.json` (<https://antigravity.google/docs/hooks/>). The event fires
once at the beginning of a conversation session and expects a flat array of command handler
objects, identical to Antigravity's `PreInvocation` and `Stop` handlers.

`registerAntigravityHook` already emits flat arrays for lifecycle events (all non-tool events
other than `PreToolUse` and `PostToolUse`), so the registration handler requires no structural change:
adding `"SessionStart": true` to `antigravityHookEvents` immediately yields valid Antigravity hook
configurations. Enabling `SessionStart` and the bootstrap hook provides automatic, transparent
session-start synchronization for Antigravity users, achieving parity with Claude Code, Codex, and Kiro.

**Alternatives rejected.**
- *Keep Antigravity on scheduled timer sync only.* Now that Antigravity natively supports `SessionStart`,
  relying solely on external background cron timers or manual sync is unnecessary and leaves Antigravity
  with worse UX than other providers.
- *Use PreInvocation for bootstrap.* Firing on every model invocation would cause continuous redundant
  sync runs on every turn, which is wasteful and alters the semantics.

**Consequences.**
- `SupportsSessionHook(ProviderAntigravity)` returns `true`.
- `EnsureBootstrapHook` installs `.gemini/config/hooks/cartographer-bootstrap/` and registers
  `cartographer-cartographer-bootstrap` under `SessionStart` in `.gemini/config/hooks.json`.
- `cartographer connect antigravity` and `cartographer sync` no longer report Antigravity as a
  hookless provider needing the scheduled sync timer.
- `cartographer disconnect antigravity` cleanly prunes the bootstrap hook and deletes `hooks.json`
  when empty, preserving non-empty user configs.
