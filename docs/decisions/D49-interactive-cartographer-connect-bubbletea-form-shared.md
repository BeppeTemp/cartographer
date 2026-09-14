---
topic: client-configurator
---

# D49 — Interactive `cartographer connect`: bubbletea form shared TUI/CLI

**Decision.** The connect form (server URL, name, token env var, auth toggle) is extracted into a
standalone bubbletea component, `connectFormModel` (`connectform.go`), reused unchanged by TUI and
CLI. A `standalone` field decides whether submit/cancel emit `tea.Quit` (command running its own
program) or stay no-op (form nested in the TUI, which reacts to `Submitted()`/`Cancelled()`).
`cmdConnect` opens the form (new `--no-input` flag as escape hatch) only if none of the four
form flags was passed explicitly and both stdin and stdout are a TTY
(`wantsConnectForm`, a pure function testable without a real `tea.Program`).
**Rationale.** The delicate constraint is "the nested form must never emit `tea.Quit`": without the
`standalone` field the TUI would exit every time the user closes the connect form. Same struct,
same `Update`, parametric quitting behavior instead of two parallel implementations.
Details: `docs/configurator.md` §`cartographer connect [provider|all]`, §TUI mode.
