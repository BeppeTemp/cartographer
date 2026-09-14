---
topic: client-configurator
---

# D35 — Configurator: opt-in interactive TUI (`--tui`)

**Decision.** The `cmd/configure` binary gains an interactive TUI mode activated by `--tui`, based on `github.com/charmbracelet/bubbletea` (+ `bubbles`, `lipgloss`). The generation business logic (Emit/Apply MCP config + `provisioning.BuildManifest`/`Apply` for skills) was extracted into a shared function `runConfigure(configureParams)`; both the flag mode (default, unchanged) and the TUI call it. The wizard collects KB/provider/transport/URL/auth/token-env/name/base-dir/dry-run with defaults from `DefaultConfig()` and a confirmation screen.

**Rationale.** AD4 called for the TUI **only** in the multi-provider configurator (never in the server): this decision implements it. The key condition is **zero duplication**: the TUI is a pure front-end that populates the same parameters as the flags and delegates to `runConfigure`, so the two modes cannot diverge. Flags remain the default (the TUI is opt-in) → no regression for scripted/CI uses. `bubbletea` is the idiomatic TUI library in Go and is pure-Go (no cgo). It adds transitive dependencies (lipgloss/bubbles/x), acceptable for an administrative binary separate from the server.

*(Superseded by D37: `cmd/configure` was deleted and the TUI recast as the flag-less dashboard of `cartographer` — `--tui` no longer exists, see `docs/configurator.md` §TUI mode.)*
