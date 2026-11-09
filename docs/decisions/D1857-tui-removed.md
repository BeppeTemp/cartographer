---
topic: client-configurator
---

# D1857 — The TUI dashboard is removed; bare `cartographer` prints a short status and the usage

**Decision.** The bubbletea dashboard that `cartographer` opened with no arguments in a terminal is removed in one release, with no deprecation notice. Bare `cartographer` in a terminal now prints a short status (the server lines `status` prints, a `next:` hint when the machine needs `setup`, one line per connected provider, a pointer to `status` and `doctor`), a blank line, then the usage; it always exits 0 and never writes. Outside a terminal it prints only the usage, as before. The three bubbletea forms of `connect` (connect, KB selection, paths) stay, with bubbletea, bubbles and lipgloss. This supersedes D175 and the dashboard parts of D37 and D1612.

**Why.** Every action of the dashboard has an identical CLI command over the same code: status is `cartographer status`, connect is `cartographer connect`, resync is `cartographer sync`, disconnect is `cartographer disconnect`. The dashboard was a second presentation layer over them, 3.2k lines with its tests, 35 commits in 90 days, and D1612 already had to fix it once when it drifted from `sync`. No installer, `setup`, onboarding or marketing path launched it, and the README never showed it (D198).

**Alternatives rejected.** A deprecation release: every action has a CLI command and the break is interactive-only, so a notice would cost a release for nothing. Keeping a read-only view: it keeps the layout and async code, which is the tax. Removing the whole bubbletea stack: replacing the three `connect` forms with line prompts is a UX decision of its own (and would save about 2.5 MB of binary), taken separately.

**Consequences.** The bare screen is a help screen, not a check: `cartographer status` stays the command with meaningful exit codes. `go mod tidy` changes no module, because the forms keep bubbletea, bubbles (`textinput`) and lipgloss. The `runSync` rule of D1612 stays true for `sync`. Release note: the keys `enter`, `s`, `S` and `d` of the dashboard are `connect`, `sync`, `sync` and `disconnect`; `r` is `status`.
