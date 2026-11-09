---
topic: client-configurator
---

# D1864 — The copilot provider covers GitHub Copilot in VS Code

**Decision.** The provider `copilot` covers the Copilot CLI and VS Code's Copilot harness, and is named "GitHub Copilot (CLI, VS Code)". Both write and read the same `$HOME/.copilot` cells, so nothing is written differently. Detection stays the `copilot` binary or `$HOME/.copilot`. VS Code's Local harness and the VS Code profile folder (`Code/User/…`) are never written to; `COPILOT_HOME` stays ignored. This amends the "not Copilot in VS Code" scope of [D676](D676-github-copilot-cli-is-a-provider.md).

**Why.** Probed on VS Code 1.145.2 (2026-11-09): the chat runs the embedded Copilot CLI runtime (`copilotVersion 1.0.103-unstable`), reads all five global cells, shows the `<server>-<tool>` tool names, delivers `postToolUse` `additionalContext` to the model and writes its sessions to `session-state/`. Detection needs no new evidence: Copilot is built into VS Code, and `globalStorage/github.copilot-chat` appears only after Copilot is first used, together with `$HOME/.copilot`, which the config directory already detects. The cost is that a VS Code install whose Copilot never ran is not detected, and a leftover `$HOME/.copilot` is a false positive as it already was.

**Alternatives rejected.** A separate `vscode-copilot` provider: two providers on the same `$HOME/.copilot` cells would split ownership and double every write. Writing the Local harness's profile `mcp.json` and prompt files: another tool's profile storage. VS Code storage and extension directories, an `AppDirs` bundle or `code` on PATH as detection: redundant with `$HOME/.copilot`, one `AppDirs` path per GOOS cannot name Insiders, and an installed editor is not evidence that Copilot is used.

**Consequences.** The workspace cells VS Code reads in a trusted workspace (`.github/agents`, `.github/hooks`; `.mcp.json` is not loaded with default settings) are not projected by this decision. The provider id, lockfile and plugin `agent.format` value `copilot` do not change. A change to the Copilot cells has to keep both clients working.
