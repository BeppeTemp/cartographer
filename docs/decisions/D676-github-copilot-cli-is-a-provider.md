---
topic: client-configurator
---

# D676 — GitHub Copilot CLI is a provider, with a hook file of its own

**Decision.** `copilot` (provider id `copilot`, "GitHub Copilot CLI"; not Copilot in VS Code) is a provider with all
five global cells: `~/.copilot/mcp-config.json`, `copilot-instructions.md`, `skills/<name>/`, `agents/<name>.agent.md`
and `cartographer-hooks/<name>/`. A hook is registered as its own file `~/.copilot/hooks/cartographer-<name>.json`, returned as a
managed file (the hook's own files are kept out of `hooks/`, which Copilot parses recursively: a non-Copilot JSON there logs an error at every session start) so a prune deletes it (the OpenCode plugin pattern); `~/.copilot/settings.json` is never patched. Only
`SessionStart`, `PreToolUse` and `PostToolUse` are registered. In a workspace only `AGENTS.md` and `.agents/skills/`
are projected; project mcp, agent and hook are unsupported. Paths are `$HOME/.copilot`: `COPILOT_HOME` is not honoured.

**Why.** Every cell was probed on 1.0.94 with `COPILOT_HOME` on a scratch directory. The project mcp, agent and hook
locations are documented but were not observed working without folder trust, and a destination is declared, never
invented. Copilot has no hook matcher, so a hook that declares one cannot keep its meaning: it is installed and not
registered, with a warning. The write-findings hook therefore declares none for Copilot and filters on the tool name
in its command (`<server>-<tool>`); its channel is `--channel copilot`, `{"additionalContext": …}` at the top level of
stdout, exit 0. The MCP entry needs `"tools": ["*"]`, or a non-interactive session is offered no tool.

**Alternatives rejected.** Patching `settings.json` inline hooks: it carries the user's own configuration, and a
dedicated file is owned whole. Registering a matcher hook anyway: it would fire for every tool. Honouring
`COPILOT_HOME`: no provider reads a home override today, and one provider doing so would split the base-dir rule.
Counting MCP calls in usage: they name no managed artifact, so only skill activations are read.

**Consequences.** The connect form listed Crush without being able to select it (`formProviders` lacked it, so its
toggle wrote nothing); both are now in the list and a test pins every agent field to its provider. The textual
`textResultForLlm` carries a second JSON object after the response with no separator, which the findings parser
already decodes value by value. Hook events, a matcher and the project cells are added by a probe, not by analogy.
