# MCP transport and authorization

## Transports

| Transport | Interface | Authorization |
|---|---|---|
| stdio | Newline-delimited JSON-RPC 2.0 | Process/user boundary; no bearer token |
| HTTP | `POST /mcp`, `/mcp?kb=<name>` or `/mcp/<name>` | Optional static bearer token |

HTTP requests return complete JSON-RPC responses. Cartographer does not expose
the legacy two-endpoint SSE transport or an HTTP streaming session.

The HTTP listener sets its own connection timeouts: a 15s header deadline, a
60s request-read deadline and a 120s keep-alive idle deadline. There is
deliberately **no** write deadline — it would also cap a legitimately slow tool
call, such as a full reindex or a git sync, and those are bounded per operation
instead. Read them as protection against connections that never complete, not
as a request budget.

`GET /health` reports service readiness. `GET /.well-known/oauth-protected-resource`
publishes RFC 9728 Protected Resource Metadata, unauthenticated, so clients can
discover the protected resource; `resource` and `authorization_servers` both
name this server's own externally-visible base URL (scheme + `Host`, from the
request itself), since it validates its own static bearer tokens rather than
delegating to a separate authorization server. Cartographer does **not**
implement an OAuth authorization server, dynamic client registration or JWT
validation; configured tokens are opaque static bearer values.

### Protocol versions: two eras at once

The server answers two generations of the protocol simultaneously (D128), and
decides which one a request belongs to from the request itself — nothing is
negotiated and nothing is remembered:

| Era | Identified by | Shape |
|---|---|---|
| handshake (`2024-11-05`) | neither marker below — including an `MCP-Protocol-Version` header naming an *earlier* revision | `initialize`/`ping` available; results exactly as before |
| `2026-07-28` | `params._meta.io.modelcontextprotocol/protocolVersion`, **or** an `MCP-Protocol-Version` header whose value is exactly `2026-07-28` | `resultType` + `_meta` server info on every result |

The header selects the new era by its **value**, not by being present (D133).
Clients on earlier revisions send `MCP-Protocol-Version` too — the header
predates this revision — and they must keep landing in the handshake era, since
they send none of the mirrored headers that era requires.

A handshake-era client sees byte-identical responses to the ones it saw before
`2026-07-28` was served at all. `server/discover` answers in both eras and
reports the supported versions, the capabilities and the server identity; it is
the method to probe with, and the one the CLI client's reachability check uses.
`initialize` and `ping` remain available for the handshake era and will only be
retired once clients have actually moved off them.

A `2026-07-28` request naming a version the server does not serve is refused
with `-32022`, whose `data.supported` lists the versions it does.

### What a `2026-07-28` request must carry over HTTP

Three headers mirror the body and are validated against it — a disagreement, or
a missing one, is HTTP 400 with JSON-RPC `-32020`:

| Header | Must equal |
|---|---|
| `MCP-Protocol-Version` | `params._meta.io.modelcontextprotocol/protocolVersion` |
| `Mcp-Method` | the JSON-RPC `method` |
| `Mcp-Name` (on `tools/call`) | `params.name`, `=?base64?…?=` form decoded first |

The headers exist for intermediaries that route without parsing a body. They
never become an input to an authorization decision: the body stays the only
thing the server acts on, which is the point of validating them at all.

Status codes in the `2026-07-28` era: unknown method → **404**, header mismatch
or unsupported version → **400**, `GET`/`DELETE` on the MCP endpoint → **405**.
In the handshake era every JSON-RPC error is still carried inside HTTP **200**.

`Mcp-Session-Id` and `Last-Event-ID` are ignored if sent, and never issued.

### Origin

The MCP endpoint validates the `Origin` header. A request without one — every
non-browser client — is unaffected. With one, it must appear in
`mcp.allowed_origins` (`CARTOGRAPHER_MCP_ALLOWED_ORIGINS`); when that list is
empty, the default, only the request's own `Host` is accepted. `"*"` in the
list disables the check. A refused origin gets HTTP 403 before authentication
runs, and the CORS allow-origin response header echoes the accepted origin
rather than a wildcard.

Operators exposing Cartographer to a browser should list the origins
explicitly: the same-origin default keeps a local browser working, but a
rebound DNS name arrives with `Origin` and `Host` both naming the attacker, so
it matches.

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

## Roles and fine-grained permissions

Scopes authorize a whole KB. Roles narrow that down to maps, journals and
concept types (D118). A role is a named set of allow rules; a token references
roles by name:

```yaml
auth:
  roles:
    - name: runbook-editor
      rules:
        - kb: homelab
          access: rw
          maps: [infra]
          types: [Runbook]
        - kb: reference
          access: r
  tokens:
    - token: ${CARTOGRAPHER_TOKEN}
      id: ci
      roles: [runbook-editor]
```

Within a rule, empty `maps`, `journals` and `types` are wildcards and non-empty
selectors are intersected: the rule above allows writing `Runbook` concepts
under `infra/` and nothing else. There are no deny rules — permissions are
unioned, so evaluation is order-independent and adding a role can only widen a
principal's access. Roles and legacy `scopes` may coexist on one token and are
unioned, so a deployment migrates one token at a time.

`id` is a stable principal identifier for logs. When omitted it is derived from
a digest of the token; a plaintext token prefix is never used.

Configuration is validated at startup and the server refuses to start on a
duplicate role or principal ID, an unknown role reference, an empty KB, an
access other than `r`/`rw`, an empty or traversal selector, or a selector
declared both as a map and a journal. No diagnostic ever contains a token
value.

### How a permission is enforced

Every request carries exactly one principal, and authorization happens at a
single point in dispatch before any handler runs. Tools are classified by the
resource they address:

| Class | Tools | Rule |
|---|---|---|
| exact concept | `concept_read`, `concept_write`, `asset_*`, `service_get`, … | the concept's map/journal and type must be allowed |
| collection | `search`, `concept_list`, `atlas_overview`, `contradiction_report`, … | results are filtered per element |
| source/destination | `concept_move` | both ends must be allowed; link rewriting additionally requires whole-KB write |
| curated index | `index_patch` | the target Map/Journal must be allowed for write; the root index (no single Map/Journal of its own) requires whole-KB write even under a policy that already grants a write inside one of its Maps (D122) |
| whole KB | `snapshot`, `sync_*`, `lint`, `pr_finalize`, … | require whole-KB access, since they have no safe partial semantics |

A tool absent from the registry is denied, so a newly added tool fails closed
until its resource semantics are chosen deliberately.

Two properties are load-bearing:

- **Non-disclosure.** A forbidden exact resource returns the same generic
  `not found` as a missing one. Existence of a concept outside the perimeter is
  not observable.
- **Filtering before limiting.** Collection tools apply the permission
  predicate before the result limit, in the in-memory index, in SQLite FTS
  (which reads further ranked pages when hidden candidates would leave a page
  short) and in the vector store. A caller therefore cannot infer hidden
  concepts from short pages or shifted pagination.

Writes are re-authorized under the git lock immediately before mutating, so a
concept whose type changes between dispatch and commit cannot be written on the
strength of a stale decision. Policies are cloned when handed out: no caller
holds a mutable alias of another principal's permissions.

A token with no scopes and no roles keeps full access, and `admin` bypasses the
resolver — pre-D118 deployments are unaffected.

## Identity and audit boundaries

Bearer tokens authorize requests; they do not become git signing identities.
Git author/committer and SSH settings are configured globally or per KB as
described in [deployment](deployment.md).

## Operational audit

When `audit.log` is configured, every `tools/call` received over HTTP or
stdio records **two** events (D119), including one an authorization decision
denies (D132): an *attempt* before the tool would run (or before the denial is
returned) and a *completion* after it, carrying the tool name, the KB, the
transport, the principal and the outcome (`success`, `application_error`,
`internal_error`, `unauthorized`, `unknown_tool`, …). An attempt with no
matching completion is itself evidence: a crash mid-operation becomes visible
rather than silent. The principal is read from the request context, so it is
always the identity authorization actually used. Arguments of an unregistered
tool are never recorded.

Entries form a JSONL hash chain with optional Ed25519 signatures: altering one
recorded entry invalidates every entry after it.

Two failure modes, selected by `audit.mode`:

- `best_effort` (default): a failed append is counted and logged, and the MCP
  call proceeds. Availability wins; the log may have gaps.
- `required`: a failed attempt-phase append rejects the call **before the tool
  runs**, so the log can never be missing an operation that actually happened.
  The sink recovers on its own once writes succeed again.

Segments rotate at `audit.max_segment_bytes` into `audit.archive_dir`. A
rotated segment is recorded in a signed checkpoint index *before* retention may
delete it, so `audit.retention_days` never breaks verifiability: the chain
stays checkable across segments no longer on disk, which `audit verify` reports
as checkpoint-only.

The operator commands are offline by design — they read the files, not a
running server, because an audit trail is most needed when the server is down:

```
cartographer audit verify --log /var/lib/cartographer/audit.jsonl [--public-key <hex>]
cartographer audit export --log /var/lib/cartographer/audit.jsonl --out report.json
```

`verify` exits non-zero on a broken chain; `export` refuses to write a report
for a chain it cannot verify, so an exported document is never
authoritative-looking without being authoritative. Without `--public-key` the
chain is checked but signatures are not, and the unsigned count is reported so
the two situations stay distinguishable.

## Stateless behavior

Authorization and optimistic content hashes do not depend on an MCP session.
Per-KB conflict and provisioning state is stored outside versioned concept
content where required.

Since the `2026-07-28` revision this is the protocol's own model, not just a
local choice: sessions, `Mcp-Session-Id`, the GET stream endpoint and SSE
resumability were all removed from the spec. Nothing is minted, stored or
echoed per connection — including the protocol era, which is derived from each
request and discarded with its response.

## Client roster

`GET /clients` reports which clients are talking to this server and on which
protocol era. It requires a bearer token whenever auth is enabled — unlike
`/health` and `/ready` it is deliberately **not** on the public-path list
(`internal/auth`), because client names and versions describe deployment
topology.

One row per distinct (KB, client name, client version, protocol version, era)
combination, with a request count and a last-seen timestamp; rows are sorted by
KB, then client name, then version, so consecutive polls are diffable. A
multi-KB server answers with one flat array across every mounted KB, each row
naming its `kb`. An `overflow` counter appears only when it is non-zero.

```json
{"clients":[{"kb":"homelab-wiki","client_name":"claude-code","client_version":"2.1.0",
             "protocol_version":"2026-07-28","era":"2026-07-28","count":42,
             "last_seen":"2026-08-14T21:40:00Z"}]}
```

Three properties bound what the roster is good for:

- **Identity is self-reported and unverified.** It comes from the client's own
  `clientInfo` — `_meta.io.modelcontextprotocol/clientInfo` in the
  `2026-07-28` era, `initialize`'s `params.clientInfo` in the handshake era.
  It is an operational aid and must never become an authorization input.
- **It is process-local and lost on restart.** Nothing is written to disk; the
  audit log remains the durable record. A handshake-era request that is not
  `initialize` carries no identity at all and is counted under the client name
  `unknown`, so legacy traffic stays visible instead of disappearing.
- **It is bounded.** At most 64 distinct keys are tracked and each identity
  field is truncated to 64 bytes; further distinct keys increment `overflow`
  rather than growing the map. Recording happens only after authorization
  succeeds, and never affects the `/mcp` response.

The roster is also the go/no-go evidence for retiring the handshake era: it is
what tells an operator whether every client of a given deployment has moved.

## Tool namespace discovery

`GET /health` reports, per mounted KB, the **effective** tool-name prefix under
which that KB's tools are registered (`tool_prefix`, empty when unprefixed —
the default). This is the authoritative source for any client that needs to
call a tool by name (D120).

Clients must not re-derive the prefix from the KB name: `tool_prefix` is an
arbitrary operator-chosen string, so a derived value is a guess that produces
calls to tools that do not exist. The value is read live per operation and
never persisted, so an operator changing a prefix server-side does not require
any client-side reconnection.

Prefixing is exact, not additive: on a prefixed KB the bare tool name does not
resolve. It applies uniformly to every tool the KB registers, including the
ones hidden by the `agent` tools profile, so the advanced/operator tools stay
reachable by name under the same namespace.
