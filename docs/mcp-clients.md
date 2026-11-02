# Using the server from any MCP client

## Who it is for

A host that gives the model only MCP tools: Open WebUI, Amazon Bedrock AgentCore Gateway with an MCP server target, an agent loop written on an MCP SDK. Such a host has no Cartographer client side: no `cartographer connect`, no skills as files, no hooks, no instructions block in a steering file. It connects to the **same server** as a provisioned harness client (Claude Code, Codex, ...) that [`cartographer connect`](configurator.md) configures, and both work at the same time. The default connection is the generic one; a provisioned client declares itself in its URL.

None of these hosts is documented to forward `initialize.instructions` to the model, and none lists prompts or resources to it, so everything the model needs is also reachable through tools it can call.

## Connect

Streamable HTTP, a `POST` to `https://kb.example.com/mcp/routed`, with the header `Authorization: Bearer <token>`. `?kbs=kb-a,work-kb` narrows the KBs the connection uses; without it the connection uses every KB the token reads. HTTP is stateless: `initialize`, `tools/list` and `tools/call` are each one self-contained POST. The transports, the binding rules and what a 401 means are in [transport-auth.md](transport-auth.md) (§Transports, §One routed topology, §Enabling bearer authentication).

## One token per integration, with an `id`

Give each integration its own token. Its `id` is the git author name of the integration's writes and the `Cartographer-Token` trailer of the commit, so the history says which integration wrote; its scopes (`kb:<name>:r` or `kb:<name>:rw`) decide what the host sees and may do. Only the YAML form of `auth.tokens` can carry an `id`:

```yaml
auth:
  tokens:
    - token: ${TOKEN_DOCS_BOT}
      id: docs-bot
      scopes: [kb:kb-a:rw]
    - token: ${TOKEN_READER}
      id: reader
      scopes: [kb:kb-a:r]
```

See [transport-auth.md](transport-auth.md) §Per-KB scopes and §Identity and audit boundaries, [deployment.md](deployment.md) §Configuration: flags, env, YAML, and [config.example.yaml](https://github.com/BeppeTemp/cartographer/blob/main/config.example.yaml).

## What the model receives

- **The operating guide in `initialize.instructions`**: the KBs the token can use, how to pass `kb`, orientation and write habits, the procedures (`ingest`, `conflict-resolve`, `import`), how to create the first map on an empty KB and each KB's own `instructions.md`; it says it is the whole guide, so an agent that received it does not call `kb_guide` for it again. A host that does not forward it to the model should paste it into the system prompt.
- **The `kb_guide` tool**: returns the same guide (including the procedures list) and, with `procedure`, the steps (`ingest`, `conflict-resolve`, `import`) as text the model can read. It is the fallback for a host that does not show `initialize.instructions` (D1100).
- **A `tools/list` shaped by the token**: only the KBs it reads, and no write tool for a token that cannot write.
- **A first page the write gate accepts**: `concept_write` takes `index_entry` so the first page of a new map is indexed in the same write.

The tools and the gate are in [control-plane.md](control-plane.md).

## Who is the author

The token `id`. A host that can send a per-user header sets `Cartographer-Author: Name <email>` and the commit is authored as that person; the format and validation are in [control-plane.md](control-plane.md) (the "Who wrote it" paragraph).

## Hosts

The names of the settings below are generic: host UIs change.

- **Open WebUI**: add an MCP server over Streamable HTTP with bearer authentication, the URL above and the token.
- **AgentCore Gateway**: an MCP server target whose outbound authorization sends `Authorization: Bearer <token>`. The gateway snapshots `tools/list` when the target is synchronized, so synchronize again after adding a KB, changing a token's scopes or upgrading the server. It may prefix tool names with the target name; the guide names tools without a prefix.
- **An SDK agent loop**: put `initialize.instructions` into the system prompt when the SDK does not (when it cannot, call `kb_guide` first), and pass tool results back to the model unmodified.

## Limits

- No skills as files: they are served as text through `kb_guide`, and the KB's own skills are read with `artifact_read`.
- No hooks and no subagents.
- No `{{repo:...}}` / `{{path:...}}` resolution: the placeholders are kept verbatim.
- No folder-to-KB scoping: the URL and the token decide.
- No per-user commit author unless the host sends `Cartographer-Author`.
- No live refresh: a KB added or a token changed shows up when the host re-reads `tools/list`.
- Everything operator-side (the health system, service restarts, re-authoring) is done on the server host.

## Do not copy a provisioned entry

An entry written by `cartographer connect` or `sync` carries `provisioned=1`: that client has the managed instructions block, so it receives no guide in `initialize`. A URL copied from such an entry into a generic host loses the guide. See [transport-auth.md](transport-auth.md) and [sync.md](sync.md).

## Check it with `curl`

```bash
URL=https://kb.example.com/mcp/routed
H='Content-Type: application/json'
curl -s -X POST "$URL" -H "$H" -H "Authorization: Bearer $TOKEN" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"check","version":"1"}}}'
curl -s -X POST "$URL" -H "$H" -H "Authorization: Bearer $TOKEN" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
curl -s -X POST "$URL" -H "$H" -H "Authorization: Bearer $TOKEN" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"kb_guide","arguments":{}}}'
```

The first answers with `result.instructions`, the guide. The second lists only the tools the token may use, none with a `kb` property when it reads one KB. The third returns the guide as text, with the names of the procedures. The executable version is [`24_generic_mcp_client.sh`](https://github.com/BeppeTemp/cartographer/blob/main/test/e2e/scenarios/24_generic_mcp_client.sh).
