# MCP transport and authorization

## Transports

| Transport | Interface | Authorization |
|---|---|---|
| stdio | Newline-delimited JSON-RPC 2.0 | Process/user boundary; no bearer token |
| HTTP | `POST /mcp`, `/mcp?kb=<name>` or `/mcp/<name>` | Optional static bearer token |

HTTP requests return complete JSON-RPC responses. Cartographer does not expose
the legacy two-endpoint SSE transport or an HTTP streaming session.

`GET /health` reports service readiness. The server also publishes RFC 9728
Protected Resource Metadata so clients can discover the protected resource.
Cartographer does **not** implement an OAuth authorization server, dynamic
client registration or JWT validation; configured tokens are opaque static
bearer values.

## Enabling bearer authentication

Tokens can be configured through server YAML or
`CARTOGRAPHER_TOKENS`/`--tokens`. `CARTOGRAPHER_AUTH` has three modes:

- unset: enable authentication when tokens exist;
- `true`: require authentication and fail startup if no token is configured;
- `false`: disable authentication.

Send a token only in `Authorization: Bearer <token>`, never in a URL.

## Per-KB scopes

A token may carry `kb:<name>:r` or `kb:<name>:rw` scopes. The KB name is its
explicit `kbs[].name`, or the normalized repository/directory basename.

YAML uses a token object:

```yaml
auth:
  tokens:
    - token: ${CARTOGRAPHER_TOKEN}
      scopes:
        - kb:homelab:rw
        - kb:reference:r
```

The environment/flag form is
`token|kb:homelab:rw;kb:reference:r`. A legacy token with no scopes has full
access to every mounted KB.

For HTTP requests:

- protocol methods such as `initialize`, `tools/list` and `ping` require read
  access;
- tools marked read-only require `r`;
- every other tool requires `rw`;
- an unknown tool or unreadable request body fails closed as a write.

The guard restores the request body after inspection so the MCP handler sees
the original JSON-RPC payload.

## Identity and audit boundaries

Bearer tokens authorize requests; they do not become git signing identities.
Git author/committer and SSH settings are configured globally or per KB as
described in [deployment](deployment.md).

`internal/audit` provides a JSONL hash-chain and optional Ed25519 signatures,
but the HTTP/stdio tool execution path does not currently append tool calls to
that log. Do not treat configured audit files as a complete request audit
trail.

## Stateless behavior

Authorization and optimistic content hashes do not depend on an MCP session.
Per-KB conflict and provisioning state is stored outside versioned concept
content where required.
