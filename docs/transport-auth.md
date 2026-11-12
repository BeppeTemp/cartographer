# MCP transport and authorization

## Transports

| Transport | Interface | Authorization |
|---|---|---|
| stdio | Newline-delimited JSON-RPC 2.0, one session per process | Process/user boundary; no bearer token |
| HTTP | `POST /mcp`, `/mcp?kb=<name>`, `/mcp/<name>` or `/mcp/routed` | Optional static bearer token |
| HTTP (read-only UI API) | `GET /api/ui/v1/…` | Same bearer token; never public |

HTTP requests return complete JSON-RPC responses. Cartographer does not expose
the legacy two-endpoint SSE transport or an HTTP streaming session, and issues
no session id: over HTTP every POST stands alone.

Over stdio the connection *is* the session. The agent client owns the pipe's
lifetime and Meridian tears the session down when stdin closes, so a request
written to a pipe that closes immediately — `echo '…' | cartographer serve` —
races its own response. A script driving the stdio server must hold stdin open
until it has read the reply. Requests on one stdio session are served
concurrently; a client that needs ordering waits for each response, as the
JSON-RPC id is there to allow.
A line that is not a valid message never ends the session: stdio answers it with the same errors as HTTP (`-32700` for a line that is not JSON, `-32600` for an empty batch, a `jsonrpc` other than `2.0` or an `id` that is not a string or a number; `id` is `null` unless the request carried a valid one) and reads the next line. Only an I/O error on stdin or stdout ends it, and a line over 16 MiB (far above the HTTP body limit), the one frame that is refused by ending the session. One message per line: a message spread over several lines is no longer accepted.

EOF on stdin is not an abort: the requests read before it are answered, then the process exits with status 0. Meridian holds the end of input back until every request id it read has a response written (a `notifications/cancelled` retires its target), for at most 30 seconds; a handler still running after that is abandoned and the process exits. A piped smoke test (`cartographer serve < requests.jsonl`) therefore gets all its answers. The wait is on responses only: a notification has none, so one still running at EOF is not waited for.

The HTTP listener sets its own connection timeouts: a 15s header deadline, a
60s request-read deadline and a 120s keep-alive idle deadline. There is
deliberately **no** write deadline — it would also cap a legitimately slow tool
call, such as a full reindex or a git sync, and those are bounded per operation
instead. Read them as protection against connections that never complete, not
as a request budget.

The stdio bridge (`cartographer mcp`) mirrors that: its HTTP client has no overall
timeout, and a call is bounded by the session (stdin closing, or the process being
stopped); after stdin closes, calls still in flight get 30 seconds to finish before they are aborted. It sends `MCP-Protocol-Version` from the version Meridian answered in
`initialize` (or, for the handshake it answers itself, a revision it knows), never
from the client's request alone. It reads stdin up to 16 MiB per line without
holding more, and a line over that ends the session with "message too large", also
while it waits for the client's roots.

`GET /health` reports service readiness; it stays public (never a 401, an invalid token is read as anonymous), but its body depends on the token (D1096): without a valid one it carries no `kbs`, with one it lists the KBs that token reads. `GET /ready` needs a valid token once auth is on, and its `503` body counts and names (`degraded`) only the KBs that token reads (#1109). `GET /.well-known/oauth-protected-resource`
publishes RFC 9728 Protected Resource Metadata, unauthenticated, so clients can
discover the protected resource; `resource` and `authorization_servers` both
name this server's own externally-visible base URL (scheme + `Host`, from the
request itself), since it validates its own static bearer tokens rather than
delegating to a separate authorization server. Cartographer does **not**
implement an OAuth authorization server, dynamic client registration or JWT
validation; configured tokens are opaque static bearer values.

`/api/ui/v1` is the read-only JSON surface the web UI consumes
(→ [`control-plane.md`](control-plane.md) §Read-only UI API). It is **not** a
public path: it passes through the same origin check and token middleware as
`/mcp`, is filtered with the same principal, and answers `404` — never `403` —
for a KB or concept the principal cannot see, so it cannot be used to probe for
existence. Only `/health`, the RFC 9728 metadata and the embedded UI's static bundle (`/ui`, `/ui/*`) are exempt from
requiring a token (`/ready` is not), and so is `GET`/`HEAD /api/v1/openapi.json`, the public API's OpenAPI
document (static, no KB data; any other method on it needs a token like every `/api/v1` path); with auth on the middleware still reads one there, and attaches
the principal of a valid bearer and no principal otherwise, never the local admin (D1096).

### One routed topology (D288)

A multi-KB HTTP server always serves `/mcp/routed`, and that is the only endpoint
`cartographer connect`/`sync` write into an agent client. The per-KB endpoints
(`/mcp?kb=<name>`, `/mcp/<name>`) stay as **plumbing**: the routed mount
dispatches into the per-KB servers, and a client whose entries were written earlier keeps working.
The routed mount ([D187](decisions/D187-one-tool-surface-for-a-multi-kb-server-a-routed-mount.md))
is not a choice: `mcp.mount_mode`, `CARTOGRAPHER_MCP_MOUNT_MODE` and `--mount-mode` do not
exist (D325; a leftover key in YAML is silently ignored).

| Endpoint | Role | `tools/list` | Selecting the KB |
|---|---|---|---|
| `/mcp/routed` | what agent clients use | one copy, shaped per connection | a `kb` tool argument, or the URL |
| `/mcp?kb=<name>`, `/mcp/<name>` | plumbing, older clients | one full copy per KB | in the URL |

**Why one topology.** A client using N KBs on per-KB entries carries N copies of
the same tool schemas in its fixed context, on every model round-trip: each
per-KB mount advertises a full `tools/list`, so a provider bound to N KBs pays
N times what the one routed copy costs. Everything a per-KB entry gave an agent client is available on the routed
mount, except client-side permission rules that match a tool *name* per KB —
those match names, not arguments, and are lost by design. Real authorization is
per-KB scopes (D44/D45), and read auto-approval through `readOnlyHint` (D76) is
unaffected.

**URL parameters of `/mcp/routed`.** `kbs` (below), `access` (D1018, D1184: `read`, or `read-min` for the read-only connection with the minimal tool listing; empty is absent, any other value is a 400), `readonly=<kb>,<kb>` (D1409: the KBs Compass set read-only; names that are not served or not readable are ignored, never a 400, and the response carries `Cartographer-Readonly` with the names applied), and `provisioned=1` (D1097: Compass has the managed instructions block, so `initialize` carries no guide; empty is absent, any other value is a 400). Any other parameter is ignored, which is what lets a client always send `provisioned=1` to a server that predates it. Only the routed path reads the marker: `/mcp?kb=<name>`, `/mcp/<name>` and stdio always send the guide. The marker rides the request context, never the cached routed view.

**The binding travels in the URL: `/mcp/routed?kbs=kb-a,kb-b`.** Absent, the
connection serves every mounted KB. `kbs` only **narrows**, never widens:
per-KB authorization still runs at the target KB. It is a different parameter
from `kb`, which stays `400` on this path (D187's one-channel rule is about
selecting *the* KB, not the allowed set). The effective set is `kbs` intersected
with the mounted KBs (D756): a name that is not mounted (a KB renamed or removed
since Compass was synced) does not take the connection down, it is served
without it and reported in `kb_status.connection.unknown`, in the `kb_status`
hint and in the error of a call naming it, with a pointer to `cartographer sync` on a provisioned connection (a generic one is told, in words, that the integration's URL needs updating, D1101);
Meridian logs it once per distinct set. If **no** name resolves the request
is a `400` naming every unknown name and the mounted KBs (an empty set would have
no tools, and falling back to all KBs would widen the binding). Duplicates are
ignored.

**A renamed KB keeps its URL working (D772).** `kb rename` records the old name
in the Meridian state file, `<data>/.cartographer/state.json` (`kb_aliases`, `{from, to}`
items; D906). A name in `?kbs=` that is
not mounted but is the `from` of an alias whose `to` is mounted selects that KB;
a mounted name always wins over an alias, so a KB later created under an old name
is reached by its own name. The pair is reported, never silent:
`kb_status.connection.renamed` is `{old: new}`, the hint says the URL is stale
and (provisioned connection) to run `cartographer sync`, and Meridian logs it once per distinct set. A
call that names the old KB in its `kb` argument (a session that outlived the
restart still has it in its schemas) is dispatched to the new one, through that
KB's own policy, audit and git wrapper. An alias selects *which KB a URL names*
and never widens a token: scopes are not migrated (D177), so a token scoped to
`kb:<old>` is still refused at the renamed KB. An alias whose target is not
mounted stays unknown. Only the routed `?kbs=` follows aliases: the per-KB
endpoints (`/mcp?kb=<old>`, `/mcp/<old>`) keep answering that the KB does not
exist.

A KB mounted after a session started reaches it only through `cartographer sync` (plus `client bind` for an explicit binding) and a new agent session; a generic connection is told that the integration reconnects or re-synchronizes its tool list (D1101): the tool list is fixed per session and HTTP has no list-changed notification. Routed `kb_status` reports the difference in `connection` (D712).

**The schema follows the connection.** With two or more KBs in the effective set,
every tool's input schema carries `kb`, **required**, as a plain string with no enum (D1183: the size of `tools/list`
does not grow with the number of KBs; the names are the KB list of the
instructions); a
call without it, or naming a KB outside it, is refused with an error naming the
allowed KBs. With exactly one, `kb` is **omitted** from every schema and the call
dispatches to that KB. That is not inference: the URL named the KB explicitly,
exactly like `/mcp/<name>`. A call that still passes `kb` equal to that KB is
accepted; any other value is refused. A KB is never guessed when the URL left
more than one open: a default would land a write in the wrong archive on a model
slip.

**The effective set (D1096)** is the `?kbs=` set (or every mounted KB) narrowed to the KBs the principal reads. A `?kbs=` name the token cannot read is `unknown`, like an unmounted one, and an alias never discloses its target; a token that reads no mounted KB gets a denied view (`forbidden: no_kb_access` on the metadata calls, no HTTP 403, D118). A principal that writes none of the set gets a read-only token view: no write tool is listed and a write call is refused by the target KB with `read_only_token`.

**The advertised set is the union** of the effective set's KBs. A tool that only
some of them register is advertised once, and refused at dispatch for a KB that
does not, with an error naming the KB and the tool. The alternative, an
intersection, would silently hide a tool from a KB that has it because a
sibling does not. The metadata
gate (`initialize`, `tools/list`) asks whether the principal can reach at least
one KB of the connection's set; since the set is already what the principal reads, it is defence in depth.

**Everything per-KB is resolved after `kb`**: the read/write classification, the
per-KB authorization policy, the git lock and commit wrapper, and the audit
record's KB: exactly one authorization and one audit record per call, at the
target KB. The routed endpoint dispatches into the target KB's own server, so it
cannot drift from the per-KB path.

**Tools are never prefixed.** One entry cannot collide with itself, so
`kbs[].tool_prefix` and `mcp.tool_prefix_mode` (D102/D153) were removed by D325 the same
way (a leftover key in YAML is silently ignored, so an upgrade never fails to start). A KB named `routed` collides with the
endpoint's own path and is **skipped** with a warning to rename it. A `?kb=` on
the routed URL is refused with 400.

**With `auth: false` the binding is advisory**: a client can edit its own URL. It
is exactly as true of the old per-KB entries (a client can add any `/mcp/<name>`
entry). Isolation between providers needs per-provider tokens with per-KB scopes.

`GET /health` always reports `mount_mode: "routed"` and `routed_path` in HTTP
mode (a server with no KB mounted yet has nothing to route and omits both), and
carries no per-KB `tool_prefix`. `cartographer connect`/`sync` read it and
write **one** entry per provider; against an older server that does not report
`routed_path` Compass keeps writing per-KB entries. **`cartographer sync`
heals a topology change** — it rewrites the entries to the single routed one and
removes the per-KB ones; no `reconnect` is needed, and `cartographer status`
names `sync` when the configured mount differs from Meridian's.

### Protocol versions: two eras at once

The wire format is implemented by the official
[MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)
([D168](decisions/D168-the-mcp-wire-format-comes-from-the-official-sdk.md)), not by Cartographer. Meridian
answers every revision the SDK serves — `2024-11-05` through `2026-07-28` —
and decides which one a request belongs to from the request itself; nothing is
negotiated up front and, over HTTP, nothing is remembered between requests.
The two that matter here:

| Era | Identified by | Shape |
|---|---|---|
| handshake (`2024-11-05`) | neither marker below — including an `MCP-Protocol-Version` header naming an *earlier* revision | `initialize`/`ping` available; results exactly as before |
| `2026-07-28` | `params._meta.io.modelcontextprotocol/protocolVersion`, **or** an `MCP-Protocol-Version` header whose value is exactly `2026-07-28` | `resultType` + `_meta` server info on every result |

The header selects the new era by its **value**, not by being present (D133).
Clients on earlier revisions send `MCP-Protocol-Version` too — the header
predates this revision — and they must keep landing in the handshake era, since
they send none of the mirrored headers that era requires.

`initialize` and `ping` remain available to the handshake era, and stay
available as long as the SDK serves that revision — retiring it is no longer
Cartographer's decision to implement.

`server/discover` reports the supported versions, the capabilities and the
server identity, and is the method the CLI client's reachability check uses.
It exists **only in `2026-07-28`**: a handshake-era request naming it gets
`-32601`. That is the one place where the older era is visibly poorer, and the
reason the CLI client sends the newer era's metadata on every request.

A request naming a version Meridian does not serve is refused with `-32022`,
whose data lists the versions it does.

A POST's `Accept` header is not enforced (D200). Streamable HTTP asks a
client to send `Accept: application/json, text/event-stream`, and the SDK
refuses a request missing either; this server only ever answers with JSON,
so it supplies both media types itself before the SDK checks them. A client
that sends only `application/json` — Antigravity does on its notifications —
or no `Accept` at all is served normally.

`Mcp-Protocol-Version` is stripped on JSON-RPC notifications (D210). The
`2026-07-28` SDK enforces that a body carrying this header must also contain
`_meta.io.modelcontextprotocol/protocolVersion`, but notifications have no
`_meta`. Some clients (Antigravity / go-sdk v1.7.0) set the header on every
POST, including notifications; Meridian removes it before the SDK sees the
request so the notification is served as 202 Accepted. Regular requests with
`_meta` are unaffected.

### Protocol errors on `POST /mcp`

A body Meridian cannot take as a JSON-RPC request is HTTP 400 with a JSON-RPC error body, never plain text: `-32700` for a body that is not JSON (or empty), `-32600` for JSON that is not a valid request (empty batch, a `jsonrpc` other than `2.0`, an `id` that is not a string or a number, a body that is not an object), `-32601` for an unknown method (carrying the request's `id`). An unknown tool *argument* is refused as `-32602` (D1279), and so are `arguments` that is not an object and an unknown tool name (D1303). A body over 4 MiB is HTTP 413 with a plain-text body (the SDK's cap, applied to every MCP `POST`; Meridian sets no other).

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
thing Meridian acts on, which is the point of validating them at all.

Status codes in the `2026-07-28` era: unknown method → **404**, header mismatch
or unsupported version → **400**, `GET`/`DELETE` on the MCP endpoint → **405**.
In the handshake era every JSON-RPC error is still carried inside HTTP **200**.

`Mcp-Session-Id` and `Last-Event-ID` are ignored if sent, and never issued.

`Cartographer-Author` is not an MCP header and is not checked against the body:
it names the git author of the writes the request makes (D904), it decides
nothing about access, and a missing or invalid one falls back to the token
(§Identity and audit boundaries).

### Origin

The MCP endpoint, and every request that changes state on any other route (any
method but GET, HEAD or OPTIONS: `/api/component/v1/*` and its 0.28 aliases `/api/usage` and `/api/client-settings`, the writes
under `/api/ui/v1`, `/clients`), validate the `Origin` header, and the rule is fixed (D906):
a request without one — every non-browser client — is unaffected; with one, its
host and port must be the request's own `Host`, whatever the scheme (a TLS-terminating
proxy does not have to be told which one it terminated, and an Atlas behind an SSO
proxy, which serves the page and the API from one origin, keeps working, D308). A
refused origin gets HTTP 403 before authentication runs, and the CORS allow-origin
response header echoes the accepted origin rather than a wildcard. There is no list
of other origins to allow (`mcp.allowed_origins` was removed): a page of another
site has no business scripting this server. The one exception is the read API: `api.cors_origins` lists browser origins allowed to *read* `/api/v1` (exact match after normalising scheme and host case and the default port; never `*`, never credentials; `Vary: Origin`). A genuine preflight (`OPTIONS` with `Origin` and `Access-Control-Request-Method`) for `GET`, `HEAD` or `OPTIONS` from a listed origin is answered 204 before the token check, since a preflight carries no token, for any path under `/api/v1` (it reveals nothing; the real request is still authenticated). The middleware also attaches to each authenticated request a re-check (`auth.RecheckFromContext`): looked up again later, it gives the principal now in force for the same bearer, or false once the token is revoked or expired, for a request that outlives its authentication. A stream (`/api/v1/kbs/{kb}/events`) is authenticated once at the request and again at every wake-up and heartbeat against the current token generation, so a revoked, expired or narrowed token ends or narrows it; it lives at most 30 minutes. The 204 allows the request headers `Authorization`, `Last-Event-ID` and `Content-Type` only: a client that adds another (e.g. `Cache-Control`) fails the preflight in the browser. Every `/api/v1` answer carries `Vary: Origin`, and a preflight also varies on `Access-Control-Request-Method` and `Access-Control-Request-Headers`; an unlisted origin gets no CORS header and a 403 on a preflight. `/mcp`, `/api/ui/v1` and the state-changing routes never get the list (D1916). The JSON-body routes (`/api/component/v1/*` and the two aliases) also answer HTTP 415 to a body not sent as
`application/json` (a cross-site form cannot set that type without a preflight),
and refuse a body over their cap with HTTP 413 (1 MiB for a usage report, 256 KiB
for a client settings report, 64 KiB for a Surveyor heartbeat, 128 KiB for a Surveyor settings report, 32 KiB for a Surveyor runs report); the usage store itself is refused past 4 MiB.
On the versioned routes (not the aliases) the token's kind is checked too: a `client` token on a Surveyor route, or a `surveyor` token on a Compass route, is HTTP 403 `wrong_token_kind` ([control-plane](control-plane.md) §Component contract).

The same-origin rule alone does not tell a page served from a rebound DNS name
from the real one: `Origin` and `Host` then both name that name. A server that
listens on a loopback address with authentication off (the local service, D268)
therefore hardens the rule: every request on every route (`/mcp`, `/ui/`,
`/api/ui/v1`, the health routes) must carry a `Host` naming this machine
(`localhost`, an address in `127.0.0.0/8`, or `[::1]`, with any port), whether or
not it carries an `Origin`; any other `Host` gets HTTP 403. It is installed only
under that condition: with authentication on, the token is the access control,
and a listen address beyond loopback with authentication off refuses to start
(§Enabling bearer authentication). An `Origin` that carries userinfo is refused.

The MCP SDK ships a third guard of the same kind (a plain-text 403 to a loopback
listener reached with a non-loopback `Host`). It is switched off: it fires with a
valid token too, which broke every same-host reverse proxy that keeps the public
`Host` (Caddy, nginx, an auth sidecar in the same pod). The rule above is the only
one, and a refusal is always the one described here.

## Enabling bearer authentication

Tokens can be configured through server YAML or
`CARTOGRAPHER_TOKENS`/`--tokens`. `CARTOGRAPHER_AUTH` has these modes:

- unset: enable authentication when tokens exist;
- `true`: require authentication and fail startup if no token is configured;
- `false`: disable authentication.

**Off-loopback needs a token (D906).** After the mode is resolved, a server whose
listen address is reachable beyond this machine while authentication is off —
`false`, or unset with no token — does not start, and the message says why.
Loopback is a listen host in `127.0.0.0/8`, `::1` or `localhost`; an empty or
wildcard host (`:39273`, `0.0.0.0:39273`) listens on every interface. The local
service pins auth off and listens on `127.0.0.1` (D268), so it is unaffected; a
remote deployment configures tokens, which turns authentication on in the default
mode.

Send a token only in `Authorization: Bearer <token>`, never in a URL.

**Compass refuses cleartext (D1023).** A client never sends the bearer token to
a non-loopback `http://` URL: every request fails before dialing unless the
operator opted in with `server.allow_insecure_http` or `CARTOGRAPHER_ALLOW_INSECURE_HTTP=1`
(see [configurator.md](configurator.md) §Transport security). Serve a remote
deployment over `https://`. Compass also refuses to follow a redirect to a URL that fails the same check, and any https to http downgrade, even to loopback.

### What a 401 tells the client

Compass reads its token from the variable `.cartographer.yaml` names in
`token: ${NAME}` (D906), so it names that variable back when Meridian answers 401, and
distinguishes the two cases that need different fixes
([D222](decisions/D222-sync-checks-the-environment-once-and-the-401-names.md)):

- `$<TOKEN_ENV> is unset or empty, so no bearer token was sent` — nothing was
  sent; the variable is missing from the environment this command ran in;
- `the bearer token from $<TOKEN_ENV> was rejected` — a token was sent and the
  server refused it; the value, its scopes or Meridian's token list is wrong.

A caller with no variable to name (a probe before any configuration exists) gets
the generic `unauthorized (401): check the bearer token/env var`. All three keep
classifying as `unauthorized` in `cartographer status --output json` and keep
matching the client-side unauthorized check, so the wording can change without
breaking a caller.

### Validation is strict, on the effective configuration

The auth configuration is validated **after** YAML, environment and flags are
merged — the shape that will actually be served — and an invalid declaration
stops startup rather than being reinterpreted (D179). Refused:

- an empty or whitespace-only token value;
- any scope that is not exactly `kb:<name>:r` or `kb:<name>:rw`, including an
  unknown access value (`kb:docs:write`) and a missing access segment
  (`kb:docs`) — a partially valid list is refused as a whole, and valid roles on
  the same token do not excuse a malformed scope;
- an environment/flag entry with a scope separator but no token (`|kb:docs:r`);
- a reference to an undeclared role, a duplicate principal id, an unknown auth
  mode;
- an unknown token `kind`, an `expires_at` that is not an RFC 3339 timestamp or
  is not in the future (a typo must not silently disable a token), and the
  non-human rules of §Token kinds and expiry.

Validation runs for every mode, `off` included, so a latent typo does not become
an exposure the day authentication is enabled. Diagnostics name the offending
token by index or `id` and never contain a token value or a raw scope string.

When authentication is required, enforcement is carried explicitly: a
configuration that asked for it and produced no usable credential fails startup,
and a store that somehow reached the middleware empty refuses every request
instead of serving them as a local admin. `/health`, the RFC 9728 metadata
path and the UI's static bundle stay public either way, so a liveness probe keeps working.

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
`token|kb:homelab:rw;kb:reference:r`. A legacy token that declares **neither**
scopes nor roles has full access to every mounted KB — that absence is the
declaration. A token that declares restrictions and ends up with none grants
nothing: unrestricted access is never inferred from an empty policy (D179).

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

Configuration is validated at startup and Meridian refuses to start on a
duplicate role or principal ID, two tokens with one secret value, an unknown role reference, an empty KB, an
access other than `r`/`rw`, an empty or traversal selector, or a selector
declared both as a map and a journal. No diagnostic ever contains a token
value.

### Roles from a plugin

A role can take its rules from a plugin pinned in `plugins` (`docs/plugins.md` §Server data) instead of writing them:

```yaml
auth:
  roles:
    - name: runbook-editor
      from: team-data/runbook-editor
      kbs: [homelab, reference]
```

`from` is `<plugin>/<role>` and is exclusive with `rules`; `kbs` is required and names the KBs the template applies to
(a plugin cannot know them): the role becomes one copy of each template rule per KB, then goes through the validation
above like a role written by hand. Tokens reference `runbook-editor`, the operator's name. The plugin only supplies the
template: a token has the permissions only if its `roles` names this entry, an unknown or unpinned plugin, a missing role
or a `from` role named like another role is a start error, and a reload re-applies the plugins loaded at start (a pin
change needs a restart). `kb rename` reports the old name in `kbs` like one in a rule.

### Token kinds and expiry

A token has an optional `kind` (`human`, the default, `client` or `surveyor`) and
an optional `expires_at` (RFC 3339, UTC or with an offset). Every released
configuration keeps loading unchanged; a token from `CARTOGRAPHER_TOKENS` or
`--tokens` cannot carry either and is `human`.

An expired token gets the same 401 as an unknown one, so a response never says
which. The server logs it once per token, by id, and logs at startup each token
that expires within 14 days (id and date, never a value), and again on a
reload that applies.

The two non-human kinds follow one rule, the minimal one the code allows, not
"read-only": both kinds write legitimately (a Surveyor commits content, a client
is what an agent writes through). A `surveyor` or `client` token must declare
`expires_at`, must declare scopes or roles (it is never admin) and so always
names its KBs; a `surveyor` token additionally can never change artifacts,
whatever its rules say (the server forces `artifacts: r` and `secrets: none` on its permissions). The
Surveyor's tool allowlist stays its own (D1257). A read-only token, such as a
dashboard, is a `client` token with `r` scopes: read-only is the scope's
decision, not the kind's.

### A token bound to a plugin

A token may carry `plugin: <name>`, the name of a plugin pinned and enabled in
`plugins` (an unpinned one refuses the start): it is that plugin's service
identity, for a hook server that calls Cartographer back. Its principal id is
forced to `plugin:<name>/<id>` (`<id>` is the token's `id`, or the derived one),
whatever `id` says, so the audit and the `Cartographer-Token` trailer of its
commits name the plugin; the commit author is that id. No other token may have an
`id` starting with `plugin:`. The kind must be `client` and the rules of a
`client` token apply (`expires_at`, scopes or roles, never admin); a plugin
token is never `proxy: true`. Secrets are always withheld (`secrets: none`
forced). Artifacts are withheld too, unless a role rule says `artifacts: rw`
(the default of a write rule does not give them back); a legacy `scopes:` entry
never does.

### Reloading tokens and roles without a restart

A running server applies a changed `auth` section (tokens and roles) without a
restart: on `SIGHUP` (not on Windows, which has none) and by polling the config
file every `auth.reload_interval` (default `30s`; `0` stops the polling). The
poll compares a hash of the file bytes. A reload re-runs what startup runs
(YAML, environment, flags, strict validation, role compilation) and swaps the
result in whole: a request already past authentication finishes with the
principal it resolved, the next one sees the new set, and a removed token's open
MCP session fails on its next request (the transport is stateless).

A token whose `expires_at` passed after startup is left out of the new set with a
log line instead of failing the reload, so removing a leaked token is never
blocked by one that ran out. A reload that does not apply keeps the tokens in force and logs why, never a
value: the file is unreadable or deleted, fails validation (the poll retries the same content
until it changes, logging once), turns authentication
off or names another mode than the running one (both need a restart), or leaves no usable token. A
reload that changes only keys outside `auth` is ignored with one line (`restart
required for non-auth keys`). An applied reload logs the count of tokens added,
removed and changed, and the tokens expiring within 14 days, like startup. Two
reloads never run at once. Without a config file (environment and flags only)
`SIGHUP` re-reads the environment of the running process, which does not change,
so only the file route is useful in practice.

### How a permission is enforced

Every request carries exactly one principal, and authorization happens at a
single point in dispatch before any handler runs. Tools are classified by the
resource they address:

| Class | Tools | Rule |
|---|---|---|
| exact concept | `concept_read`, `concept_write`, `asset_*`, `service_get`, … | the concept's map/journal and type must be allowed |
| collection | `search`, `concept_list`, `atlas_overview`, `contradiction_report`, … | results are filtered per element |
| source/destination | `concept_move` | both ends must be allowed, and for an expanded concept so must each satellite (its own type, source and destination); link rewriting additionally requires whole-KB write |
| whole-KB write exact concept | `concept_merge` | needs the whole-KB write grant, as `concept_move` with link rewriting does: it redirects every inbound link across the KB, so a role bounded by map, journal or type is refused (`needs_whole_kb`); a token with no grant on the KB gets `not found`; `rewritten` lists only pages the caller sees |
| curated index | `index_patch` | the target Map/Journal must be allowed for write by a rule that names no type: the index lists pages of every type, so a role narrowed by type cannot rewrite it, and its own entry is written with `index_entry` on the creation, under the page's grant (D714); the root index (no single Map/Journal of its own) requires whole-KB write even under a policy that already grants a write inside one of its Maps (D122) |
| whole KB | `snapshot`, `sync_*`, `lint`, … | require whole-KB access, since they have no safe partial semantics. `secret_resolve` and `secret_set` need the secrets grant as well (D1894): a rule with `secrets: none` withholds them, and `concept_transfer` refuses a concept whose frontmatter names `secret_refs` or `secrets_source` into a KB where the token is so restricted. `artifact_write`, `artifact_delete`, `artifact_move` (on both KBs) and `skill_install` need the artifacts grant as well (#1405): a rule with `artifacts: r` grants everything its whole-KB write grants except those four, and `/api/usage`, which writes artifact metadata. It is a narrower allow, not a deny rule, so a second rule of the token without the key restores them |

A tool absent from the registry is denied, so a newly added tool fails closed
until its resource semantics are chosen deliberately.

Two properties are load-bearing:

- **Non-disclosure.** A forbidden exact resource returns the same generic
  `not found` as a missing one. Existence of a concept outside the perimeter is
  not observable. Every other denial reads `forbidden: <code>: <hint>` (D343:
  `unclassified_tool`, `bad_arguments`, `read_only_token`, `needs_whole_kb`,
  `outside_scope`, `template_unusable`, `missing_id`, `no_kb_access`,
  `no_principal`, `artifacts_read_only`, `secrets_restricted` (a rule with `secrets: none`, D1894), and `unbound_folder` for a read-only routed session, D1018, `read_only_profile` for `access=read-min`, D1184, `read_only_kb` for a KB Compass set read-only with `readonly=`, D1409, `denied_by_hook` and `hook_unavailable` for a write an operator's `write_veto` hook refused or could not judge under `fail_closed`, the `hooks:` block of `deployment.md`); the reason describes the caller's own token and arguments,
  never hidden content, and a `concept_batch` denial never names the operation.
- **Filtering before limiting.** Collection tools apply the permission
  predicate before the result limit, in the in-memory index, in SQLite FTS
  (which reads further ranked pages when hidden candidates would leave a page
  short) and in the vector store. A caller therefore cannot infer hidden
  concepts from short pages or shifted pagination.
- **Collections have no type.** Whether a map or journal is listed at all
  (`map_list`, `atlas_overview`, the UI's KB list and overview) is decided by
  the `maps`/`journals` selectors alone; the `types` selector applies to the
  concepts inside it, and the counts shown are counts of visible concepts. A
  role narrowed to a map and a type therefore sees that map, holding only its
  concepts of that type (D228).

Writes are re-authorized under the git lock immediately before mutating, so a
concept whose type changes between dispatch and commit cannot be written on the
strength of a stale decision. Policies are cloned when handed out: no caller
holds a mutable alias of another principal's permissions.

A token with no scopes and no roles keeps full access, and `admin` bypasses the
resolver.

## Identity and audit boundaries

Bearer tokens authorize requests. SSH keys and git tokens are files of the
credentials directory, a KB's own or a default one, as described in
[deployment](deployment.md) §Credentials directory (D905).

The git **author and committer** of a write are the writer, not a setting (D904,
D950). A client that knows who it acts for sends `Cartographer-Author: Name <email>`
with the request; without a valid one the writer is the identity the operator
configured for the KB in the server file (`git.identities`),
else, on a local server, the git identity of the user running it (D1974), else `<token id> <cartographer@<host>>`. Every write carries `Cartographer-Host`
and `Cartographer-Token: <token id>` trailers that Meridian appends itself.
The configured identity is attribution, never authorization. The author line is therefore a claim by the caller, and the
trailer is what Meridian knows: attribution, not a signature, since whoever
holds the token can write under any name. Format, validation and the warning on
a rejected header → [control plane](control-plane.md). A token's `author_name`
and `author_email` are removed settings: a file that still has them loads with
a warning, and they never refuse the start.

**Per-user identity from a trusted proxy (D1894).** Behind an SSO proxy the
person can be the principal. With `auth.proxy_identity` declared
(`trusted_addresses`, `user_header`, optional `groups_header` and
`groups_separator`, `group_roles`), a request whose TCP peer is in
`trusted_addresses` AND whose bearer is a `proxy: true` token may carry the
user header; the principal is then `user:<name>` (kind human, `Via` the proxy
token id) with the union of the roles its groups map to, **clamped by the proxy
token's policy** (`auth.Clamp`: a KB where the token has no whole-KB rule is
dropped, `rw` becomes `r` where the token only reads, and its artifact and
secrets restrictions carry over, so the person never sees or resolves secrets
the token cannot). The ceiling is applied per KB, so a proxy token must use
whole-KB rules only: one whose scopes or roles select maps, journals or types
is refused at load, and ids starting with `user:` are reserved. A header from another peer or on another token is
ignored, never an error; an absent header leaves the request the proxy token's
own; no mapped group is a `403`; an invalid value (user over 128 bytes,
non-printable, angle brackets, over 64 groups) is a `400`, never truncated.
The proxy must overwrite both headers on every request. Everything keyed on the
principal id (activity, usage, the author fallback, the `Cartographer-Token`
trailer) then names the person. `auth.IdentityResolver` is the hook a plugin
can fill in place of the header reader; trust and mapping apply either way.

**Identity hook.** An `identity` hook (`hooks:` in `deployment.md`; one at most, and `auth.proxy_identity` must be set)
lets an external system name the person. It is asked only for a request on a `proxy: true` token from a trusted peer
whose user header carries an identity, never for an unauthenticated or public path. The body carries `peer`,
`token_id`, `headers` (the names listed in the entry's `headers`) and `proxy_identity` (the user and groups the header
named). It answers `{"user": ..., "groups": [...]}`, which replaces the header identity, or no opinion, which keeps it.
The answer is only names: the group map and the clamp by the proxy token then run as for a header, so the hook can
never exceed the token, and the same limits apply (a violation is a `400`). The answer is cached for `cache_ttl`
(default `60s`, 4096 entries) under a key made of the token id, the header identity and the listed header values; a
failed call is not cached and the hook's circuit breaker bounds a hook that is down. A failure (including an answer that
does not parse) under `fail_open` keeps the header identity; under `fail_closed` it is a `503` with `Retry-After: 30`. A
`deny` answer is a `403`; a `dry_run` hook is only observed. The `Retry-After` is the breaker's open window
(`hooks.RetryAfterSeconds`). A `deny` answer and an answer that does not parse are never cached and do not count as a
breaker failure: only a failed call does, so a hook that keeps denying is asked again on every request. Every listed
header reaches the plugin as sent: a listed `Cookie` hands it the person's session cookie, so list only what the plugin
needs. The header identity is checked against the limits before the hook sees it.

Tokens are declared in the YAML `auth.tokens` list; the flat
`CARTOGRAPHER_TOKENS` form cannot carry the fields of a
mapping. A `token:` written as exactly `${NAME}` is read from the environment, so the file never holds the secret; an
unset or empty variable refuses startup (#680). `CARTOGRAPHER_TOKENS`, when
set, replaces the whole YAML list, so a deployment moving to YAML tokens drops
that variable.

## Operational audit

The audit log is always on (D905): every `tools/call` received over HTTP or
stdio records **two** events (D119), including one an authorization decision
denies (D132): an *attempt* before the tool would run (or before the denial is
returned) and a *completion* after it, carrying the tool name, the KB, the
transport, the principal and the outcome (`success`, `application_error`,
`internal_error`, `unauthorized`, `unknown_tool`, …). An attempt with no
matching completion is itself evidence: a crash mid-operation becomes visible
rather than silent. The principal is read from the request context, so it is
always the identity authorization actually used. Arguments of an unregistered
tool are never recorded. Of a registered tool only the fields its allow-list names
are recorded (identifiers, never content): a string, a boolean, or an array of
strings, joined and bounded in items and bytes (`joinAuditList`, `audit.go`). A completion also records `result_bytes`, the size of
the result's text (D301): what the call cost the agent to read, so read
amplification per session is measurable from the log without logging content.
The field is hashed only when present, so events written before it verify
unchanged. A write a `write_veto` hook judged adds the resources `hook` (its name) and `hook_verdict` (`deny`, `dry_run_deny` or `error`) to its pair; a veto is audited as an authorization denial (`unauthorized`).

A record also names the kind of the token behind the principal
(`principal_kind`: `human`, `client` or `surveyor`; empty when the call has no
principal) and, when a trusted proxy named a person, the id of the proxy token
that carried the request (`via`), so a write by `user:alice` through an SSO
proxy reads as the person, the kind and the bearer. Both are trailing
`omitempty` fields hashed only when present, like `result_bytes`, so events
written before them verify unchanged.

Entries form a JSONL hash chain signed with Ed25519: altering one recorded
entry invalidates every entry after it.

Verification reads the bytes on disk, not the values a JSON decoder extracts
from them: every line must be exactly the line the writer produced (the
canonical encoding of the entry, in the layout of the release that wrote it),
so a field the hash does not cover (`args`, `agent_id` on a version-2 event),
a case-variant, duplicate or unknown key, or a different spelling of the same
values makes `audit verify` and `audit export` fail, and a version-2 event with
a non-empty `args` or `agent_id` is malformed. Logs written by earlier releases
still verify.

On the routed mount (`/mcp/routed`) the routed view records the calls it
refuses itself, before any KB server sees them: the argument checks (`bad_arguments`
and the unknown-argument `-32602`), `unbound_folder`, `read_only_profile`,
`read_only_kb`, a missing `kb`, a `kb` outside the connection's set and a tool
the named KB lacks. Each is one denied attempt+completion pair with outcome
`unauthorized` (a tool missing from the target KB included); the `kb` field is
set only when the call resolved to a KB in the set, never to a name the caller
typed (an allow-listed resource such as `target_kb` of `concept_transfer` is
recorded as the caller sent it). A call the view dispatches is audited once, by
the target KB's server ([D1627](decisions/D1627-audit-verifies-the-written-form-and-the-routed-view-audits-refusals.md)).

Documented limit: the chain proves that no recorded entry was altered and that
the entries follow one another, not that the tail is complete. Whoever can
write the log file can remove its last attempt/completion pairs (or empty the
active file when there is no checkpoint) and the chain stays valid, because
each entry commits to the one before it and nothing commits to the one after;
only a cut inside a pair is noticed (an attempt with no completion). A server
started afterwards continues from the shortened tail. Keep the audit directory
writable by the Meridian user only, and copy the log to a sink Meridian cannot
write when the tail matters.

The log is `<data>/audit/audit.log` unless `audit.log` moves it, and its key is
`audit.key` in the credentials directory, which the first start generates when
it is missing; the start logs the path and the public key `audit verify`
needs. A key Meridian can neither read nor generate (a read-only directory
without `audit.key`) leaves the log unsigned under `best_effort`, with a
warning at every start, and refuses the start under `required`. A log that
cannot be opened does the same: `best_effort` runs without one and says so,
`required` does not start. There is no setting that turns the log off.

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
The in-memory state and the index file change together with the segment: a
rotation whose rename fails restores the previous index and leaves no phantom
checkpoint. An `audit.archive_dir` on another volume is handled by a
copy, fsync and truncate instead of the rename (EXDEV). On open, by the holder
of the writer lock only, a crash between the two steps is repaired: a
checkpoint whose segment never reached the archive, while the active log still
starts with its first hash, is dropped (index re-signed when a key is
configured); a checkpoint whose segment is archived while the active log still
holds the same entries (first hash, last hash, count) finishes with the
truncate. Open repairs only an index it can trust: with a key the index
signature must verify and the last checkpoint must continue the chain of the
one before, otherwise the file is left untouched for `audit verify` to report.

The operator commands are offline by design — they read the files, not a
running server, because an audit trail is most needed when Meridian is down:

```
cartographer audit verify --log /var/lib/cartographer/audit.jsonl [--public-key <hex> [--signed-since <RFC3339>]]
cartographer audit export --log /var/lib/cartographer/audit.jsonl --out report.json
```

`verify` exits non-zero on a broken chain; `export` refuses to write a report
for a chain it cannot verify, so an exported document is never
authoritative-looking without being authoritative. Without `--public-key` the
chain is checked but signatures are not, and the unsigned count is reported so
the two situations stay distinguishable. `export` to stdout exits 1 with the error
on stderr when the write fails (a closed pipe, a full disk).

With a key the log must be signed from a start: `--signed-since`, else the
first checkpoint written with a key loaded, else the first signed entry. A log
with entries and none of the three has no signature at all, so a rewrite that
recomputed the chain and dropped every signature would pass for the original:
verification (and `export`) fails with `no signed entry`. `--signed-since`
asserts that every entry at or after that time is signed (it needs
`--public-key`, otherwise exit 2); an operator whose log was never signed
passes a time after its last entry, or drops the key.

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
`/health` it is deliberately **not** on the public-path list
(`internal/auth`), because client names and versions describe deployment
topology.

One row per distinct (KB, client name, client version, protocol version, era)
combination, with a request count and a last-seen timestamp; rows are sorted by
KB, then client name, then version, so consecutive polls are diffable. A
multi-KB server answers with one flat array across every mounted KB, each row
naming its `kb`. The traffic of the routed endpoint (`/mcp/routed`, which every
synchronized client uses) is listed too, with the connection's KB set as `kb`
(comma-joined, e.g. `kb-a,kb-b`; the read-only variants of one set are merged).
A routed row is shown only when the token can read every KB of its set. Rows are limited to the KBs the token can read (any read grant
on the KB, whatever its map, journal or type selectors; an admin sees all): a
token with no grant on a KB gets neither its rows nor its activity, and an empty
roster is a 200, not a 403, so the answer does not confirm the KB exists. An
`overflow` counter appears only when it is non-zero and counts visible KBs only.

```json
{"clients":[{"kb":"homelab-wiki","client_name":"claude-code","client_version":"2.1.0",
             "protocol_version":"2026-07-28","era":"2026-07-28","count":42,
             "last_seen":"2026-08-14T21:40:00Z"}]}
```

Four properties bound what the roster is good for:

- **Identity is self-reported and unverified.** It comes from the client's own
  `clientInfo` — `_meta.io.modelcontextprotocol/clientInfo` in the
  `2026-07-28` era, `initialize`'s `params.clientInfo` in the handshake era.
  It is an operational aid and must never become an authorization input.
- **Identity belongs to a session, not to a request.** A session agrees it once
  and every later request inherits it; a client that names itself differently
  mid-session does not get a second row. Two clients are two sessions.
- **It is process-local and lost on restart.** Nothing is written to disk; the
  audit log remains the durable record. A client that sends no `clientInfo` is
  counted under the name `unknown`, so anonymous traffic stays visible instead
  of disappearing. Notifications are not counted: the roster counts what
  clients ask of this server.
- **It is bounded.** At most 64 distinct keys are tracked and each identity
  field is truncated to 64 bytes; further distinct keys increment `overflow`
  rather than growing the map. Recording never affects the `/mcp` response. It
  happens after the transport authentication and the metadata gate, but before
  the per-tool check of a `tools/call`: a call a token is denied is still counted,
  which is what an operator wants to see.

The roster also tells an operator whether every client of a given deployment
has moved to the current protocol era.

## Tool namespace discovery

A server never prefixes tool names (D288), so there is no namespace to
discover: `GET /health` carries no per-KB `tool_prefix`, and clients use the bare
tool name. Clients must not derive a prefix from the KB name.
