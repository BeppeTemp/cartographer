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

When `audit.log` is configured, every `tools/call` dispatched over HTTP or
stdio records **two** events (D119): an *attempt* before the tool runs and a
*completion* after it, carrying the tool name, the KB, the transport, the
principal and the outcome (`success`, `application_error`, `internal_error`,
`unauthorized`, `unknown_tool`, …). An attempt with no matching completion is
itself evidence: a crash mid-operation becomes visible rather than silent. The
principal is read from the request context, so it is always the identity
authorization actually used. Arguments of an unregistered tool are never
recorded.

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
