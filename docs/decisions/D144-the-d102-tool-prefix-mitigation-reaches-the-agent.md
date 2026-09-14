---
topic: transport-auth
---

# D144 — The D102 tool-prefix mitigation reaches the agent

**Decision.** The generated KB instructions block names each tool with the KB's *effective*
prefix: `BuildOptions.ToolPrefixes` carries it into `generateKBInstructions`, which renders
`<prefix>__search` and friends when set and the bare names when not. The server prints one
stderr warning at startup when it mounts 2+ KBs of which 2+ resolved to no prefix, naming
them; it never fails startup and never applies an implicit prefix. `kb_status` reports the
KB that served the call as `kb`, and `kb.Init` writes the root index title from the KB
directory name instead of the constant `Knowledge Base` (creation only; an existing
`index.md` is untouched). The prefix stays opt-in and default-off, no tool is renamed, added
or removed, and read/write classification and the `agent`/`full` profile keep matching on
the stripped name.

**Rationale.** The mitigation D102 exists to serve was unreachable in practice: turning a
prefix on made the block that Cartographer materializes into the client's memory file —
marked *managed, do not edit by hand* — instruct the agent to call five tools that do not
exist on that endpoint. Nothing warned server-side either: the only signal was client-side,
`kiro`-only, and skipped entirely by an operator who writes the client's MCP config by hand
— which is exactly how the reported incident happened, with an agent answering questions
about one KB out of another with full confidence. And once a client flattens the namespace,
`serverInfo.name: cartographer:<kb>` is already discarded by the time a tool runs, so
nothing in a tool response identified the KB; on a fresh KB even `atlas_overview` returned a
constant placeholder. Fixing the imprinting, warning from the process that knows the
configuration, and putting the KB's identity where an agent can read it closes the gap
without touching the protocol shape of the other tools. The report's alternative — one tool
set with a `kb` argument per call — is a separate design that re-opens per-connection auth
scoping (D118) and is deliberately not adopted here.
