---
topic: client-configurator
---

# D86 — Connect UX: agent subsets, 0-KB diagnostics, absolute paths

**Status: implemented (2026-07-24).**

**Context.** A healthy service with no mounted KB returned `400 kb parameter required` from
`/mcp`, which `connect` described as an unreachable local service. `connect` and `disconnect`
also accepted only one provider or `all`, despite the configurator already supporting all four
providers independently; and their success output showed paths relative to the target directory,
making a write look as if it had happened in the current directory.

**Decision.**
- **Health-aware probe.** `internal/client.Health` derives `/health` from the configured MCP URL
  and parses the additive `ready` and `kbs` fields defensively. A false `ready`, or an explicitly
  empty `kbs` list from a pre-D84 server, produces the first-KB guidance (`kb create`, then
  `service restart`); only an actually unreachable loopback service offers installation. A missing
  `ready`/`kbs` remains compatible with older healthy servers.
- **Provider subsets.** `connect` and `disconnect` accept `--agents claude,codex` as a validated
  comma-separated selection; it cannot be combined with the positional provider. The interactive
  Bubble Tea form exposes one checkbox per supported provider, preselected from the detected set.
- **Unambiguous output.** `configurator.Apply` records and returns absolute config paths while
  retaining relative paths for provider-native file lookup. A successful non-dry-run connect also
  tells the user to restart the selected agent sessions so their MCP clients reload the tools.

**Rationale.** Health is the appropriate readiness signal for a client-side onboarding decision:
it distinguishes a server that needs its first KB from one that is down without making `/mcp`'s
transport error carry product guidance. CSV and checkboxes cover the common partial-install case
without adding a new provider model, while absolute paths make the side effects of configuration
safe to verify from any working directory.
