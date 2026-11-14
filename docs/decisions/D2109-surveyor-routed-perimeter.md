---
topic: transport-auth
---

# D2109 — A Surveyor addresses a routed server through the routed endpoint and `?kbs=`

**Decision.** When `/health` says the server is routed, the Surveyor talks to the
`routed_path` it advertises with its perimeter in the URL, `?kbs=<kbs of the config>`,
whether `server_url` names `/mcp` or `/mcp/routed`. Each KB's caller then adds the KB as the
`kb` tool argument and hides that property from the tool schemas it hands to the model
(`MCPClient.WithRoutedKB`). A `server_url` that already carries `?kb=` keeps the per-KB
addressing (one KB). `client.ServerBase` strips a trailing `/mcp/routed` as it strips `/mcp`,
so the health route, the component routes and every caller that derives the base (Surveyor,
`mcp probe`, `mcp exec`) reach the server's own paths. Only a 401 or 403 from the start-up
check is reported as a rejected token.

**Why.** On a routed server `/mcp` answers `400 kb parameter required` and `?kb=` on
`/mcp/routed` is a 400 (D187), so a multi-KB perimeter had no URL that worked: only
`/mcp?kb=<kb>` did, pinning the Surveyor to one KB although `kbs:` is a list. The routed
endpoint is the one topology of the server (D288) and `?kbs=` is its way to name a perimeter.
The cost is a Surveyor-side shim for the `kb` argument, since the loop and the recipes were
written against one endpoint per KB.

**Alternatives rejected.** Keep `?kb=` per KB on `/mcp`: it is plumbing, kept only for older
clients, and a config with `/mcp/routed` still failed. Let the model supply `kb`: a model could
then address a KB outside the task's own, which the per-KB caller guarantees against. Tell the
operator to write `?kb=` by hand: it cannot express a list.

**Consequences.** The Surveyor needs no `server_url` change when a server becomes routed. The
heartbeat and run reports keep their `?kb=` query, which the component routes read on any base.
A routed server that renames a KB keeps working through `kb_aliases` as for any `?kbs=`
client. `mcp probe` and `mcp exec` on a routed server with auth on send their token because the
health probe now asks `/health`.
