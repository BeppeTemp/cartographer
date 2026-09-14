---
topic: client-configurator
---

# D201 — A provider's tool identifier limit is checked at connect and sync

**Status: implemented.** Closes #273 (problem 2).

**Context.** Antigravity shows each MCP tool to the model as `mcp_<server>_<tool>` and discards any
identifier that does not match `^[a-zA-Z0-9_-]{1,64}$`. On a multi-KB server the entry name is
`cartographer-<kb>` and each tool carries the KB's prefix ([D153](D153-a-tool-prefix-is-the-default-for-every-mounted-kb.md)), so
`mcp_cartographer-morbos-agentic-wiki_morbos_agentic_wiki__git_conflict_resolve` is 78 characters.
The session starts; those tools are simply missing from it. The server's own 48-character budget
([D102](D102-opt-in-per-kb-mcp-tool-name-prefix.md)) is on the tool name alone and cannot see the entry name.

**Decision.** `connect` and `sync` warn on stderr when an entry they write for such a provider would
exceed its limit, naming the entry and the length it reaches, with both remedies: a shorter
`kbs[].tool_prefix`, or `mcp.mount_mode: routed` ([D187](D187-one-tool-surface-for-a-multi-kb-server-a-routed-mount.md)), whose single
entry carries unprefixed tools.

- **The limit is a descriptor field**, `ToolIdentifierLimit` (64 for Antigravity), like
  `FlatToolNamespace` for Kiro: the check reads the registry, not a provider name.
- **The length is computed from evidence, not an upper bound.** Entry name, plus the KB's effective
  prefix as `/health` advertises it ([D120](D120-tool-prefix-discovery-for-client-owned-multi-kb.md)), plus the longest registered tool name
  (`mcpserver.MaxBareToolNameLen`, pinned to the real registry by a test). Assuming the 48-character
  maximum instead would warn on any entry name over 11 characters, including ones that fit. A bare
  entry on a one-KB server is computed with that KB's prefix; a routed entry without one.
- **Silent without `/health` facts**, as [D152](D152-tool-prefix-uniqueness-is-enforced-and-the-client.md) requires of the Kiro warning's verified path:
  unknown prefixes are not evidence of an overflow.
- **A warning, not a rename.** Shortening the entry name for one provider only would give the same
  KB different names across providers and change what `status`/`doctor` expect; both remedies
  already exist server-side and fix every provider at once.

**Consequences.** A deployment whose names overflow keeps working exactly as before — the check only
makes the missing tools visible. The constant must be raised when a longer tool is added; the test
fails until it is.
