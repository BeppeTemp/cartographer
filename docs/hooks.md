# Hooks: the protocol contract

Meridian calls external **hooks** at four points of the core, so that governance (a change
approval, an external audit store, a vault, a directory) can live in a plugin without the server
running third-party code. A hook is a separate HTTP service; it supplies an input or denies, and
it can never replace a decision of the core. This page is the contract a hook author builds
against; the server-side keys are in [deployment](deployment.md) §Configuration: flags, env, YAML
(the `hooks:` paragraph). Why: `D1957`.

## Transport and versioning

One transport: a `POST` of a JSON object to the entry's `url`, with these headers:

| Header | Value |
|---|---|
| `Cartographer-Hook` | the point (`write_veto`, `audit_sink`, `secret_resolver`, `identity`) |
| `Cartographer-Hook-Version` | the protocol version, an integer: `1` |
| `Cartographer-Request-Id` | 32 hex characters, also in the body |
| `Authorization` | `Bearer <the content of the entry's credential file>` |

Every request body carries `version`, `hook` (`<plugin>/<name>`), `request_id`, `time` (the server
clock, UTC, RFC 3339) and, when the point concerns one KB, `kb`; the point adds its own fields.
Every response is a JSON object with `version` and the point's fields. The call is bounded by the
entry's `timeout` (default 2 s, at most 10 s) over connect, headers and body; TLS is verified, a
redirect is never followed (a `3xx` is a failure), `http://` is accepted only to a loopback host,
and a response body is read up to 64 KiB.

**Versioning.** The version is an integer. A field added to a request or a response does not change
it, and unknown fields are ignored in both directions, so a hook written against version 1 keeps
working as fields are added. A breaking change is a new version with a version-suffixed point; the
server then supports the version it speaks and the one before it. A response whose `version` is not
the request's is a failure.

**Answers are input or deny.** Where a point decides, the answer is `"decision": "deny"` (with an
optional `reason`) or anything else, which is **no opinion**: `allow` is no opinion too, so a hook
can only turn a core allow into a deny. The core decides first: a hook is never asked about a
request the core refused. A `reason` is cut to 200 printable characters and only ever shown to the
caller inside the coded denial (and, for a hook in `dry_run`, in the server log line that records what it would
have denied); no hook output reaches a commit, a file path or a shell.

The JSON Schema of each point's request and response is published in
`docs/schemas/hooks/` (one file per point, such as [`write_veto.json`](schemas/hooks/write_veto.json), with
`$defs/request` and `$defs/response`);
`internal/hooks/hooktest` validates bodies against them and serves a recording stand-in for a hook
in tests (`TestRecordedRequestsValidate`, `TestDocsHookExamplesValidate`).

## Failure policy, circuit breaker, dry run

A failure is a timeout, a connection error, a non-`2xx` status, a body that is not a JSON object or
is over 64 KiB, or a wrong `version`. What it means is the operator's choice, required in every
entry (`failure_policy`, no default):

| `failure_policy` | `mode: enforce` | `mode: dry_run` |
|---|---|---|
| `fail_closed` | the request is denied | logged only |
| `fail_open` | the request continues, the failure is logged | logged only |

After 5 consecutive failures a hook's circuit opens for 30 s: the policy applies without calling it,
then one probe call decides whether it closes. A dead hook costs five timeouts, then one probe every 30 s, not one
timeout per request. A
call the caller cancelled is not a failure of the hook (the breaker is untouched), but it has no
answer, so the policy still decides. `mode: dry_run` calls the hook and records what it would have
decided (`dry_run_deny`) without ever acting on its answer. A `secret_resolver` must be
`fail_closed` and refuses `dry_run`; an `audit_sink` ignores `mode`. Failures and circuit
transitions are logged with the hook name, never a body, a URL query or a credential (a dry-run deny logs the
hook's reason).

## The four points

### `write_veto`

Asked for every tool that writes, after the core has authorized the call (an admin included) and
**before the handler, outside the KB lock**: a slow hook delays its own request and no other writer.
The hooks that cover the KB (`kbs`) are asked in config order, stopping at the first deny; a tool
that writes into a second KB (`concept_transfer`, `artifact_move`) is judged on both. The limit: the
veto judges the request, not the final diff, and state can change between the veto and the lock;
the content-aware check is the core's write gate. A `/mcp/routed` write asks the hook once.

| Request field | Meaning |
|---|---|
| `kb` | the KB written |
| `principal` | `id`, `kind` (`human`, `client`, `surveyor`), `via` (the proxy token, when a trusted proxy named the person), `groups` |
| `tool`, `class` | the tool name, and `"write"` |
| `target` | the audit's allow-listed identifiers of the call (concept id, map, artifact path): never frontmatter, a body, a patch value or a secret name |
| `reason` | the caller's `reason` argument |

| Response field | Meaning |
|---|---|
| `decision`, `reason` | `deny` refuses with `forbidden: denied_by_hook: a hook the operator configured refused this write: <reason>`; anything else is no opinion |

A failure under `fail_closed` refuses with `hook_unavailable`. The audit pair of the call carries
`hook` and `hook_verdict` (`deny`, `dry_run_deny`, `error`).

<!-- hook-example: write_veto request -->
```json
{"version":1,"hook":"acme/review","request_id":"0123456789abcdef0123456789abcdef","time":"2026-01-01T10:00:00Z","kb":"work-kb","principal":{"id":"user","kind":"human","via":"","groups":["staff"]},"tool":"concept_write","class":"write","target":{"id":"runbooks/restart"},"reason":"fix the restart order"}
```

<!-- hook-example: write_veto response -->
```json
{"version":1,"decision":"deny","reason":"runbooks are frozen until the change window"}
```

### `audit_sink`

Receives the audit chain as it is written, in chain order and **at least once**, from a cursor: the
hash of the last entry it acknowledged, persisted beside the log, so a restart resumes where it was.
Each event is the entry as the log stores it, `hash` and `sig` included, so the receiver can verify
the chain; its `id` is unique, for deduplication (an entry written by a release before the versioned events,
`version` absent, has no `id`: its `hash` identifies it). Delivery runs in its own goroutine and never slows
an append; a failure retries with a backoff from 1 s to 60 s, and nothing is lost (the local chain
is the source of truth). `gap: true` says that entries after the cursor were deleted by retention, or that
the cursor is not a hash of this log (a stale or foreign cursor file): delivery resumes at the oldest retained
entry. The cursor is the chain hash, not a `changes_since`
position, because the chain also records reads, denials and refused attempts (`D1991`).

| Request field | Meaning |
|---|---|
| `events` | at most `batch_size` (default 100) audit entries |
| `first_hash`, `last_hash` | the hashes of the first and the last entry |
| `gap` | entries after the previous cursor were removed by retention, or the cursor is not in this log |

| Response field | Meaning |
|---|---|
| `ack` | must equal `last_hash`: only then does the cursor move |

**Blocking mode** (`blocking: true`) makes every call wait, after its attempt event and outside the
KB lock, up to the hook's `timeout` for the hook to acknowledge it; a `fail_closed` sink that did not
refuses the call and closes the pair with `audit_unavailable`. It trades availability for the
guarantee that no call runs without an external copy: a dead blocking `fail_closed` sink refuses
every call. See [transport-auth](transport-auth.md) §The audit sink.

<!-- hook-example: audit_sink request -->
```json
{"version":1,"hook":"acme/siem","request_id":"0123456789abcdef0123456789abcdef","time":"2026-01-01T10:00:00Z","events":[{"version":2,"id":"5f2b","timestamp":"2026-01-01T09:59:59Z","phase":"attempt","request_id":"9a1c","principal_id":"user","transport":"http","kb":"work-kb","tool":"concept_read","read_only":true,"resources":{"id":"runbooks/restart"},"outcome":"","prev_hash":"genesis","hash":"3b7e","principal_kind":"human"}],"first_hash":"3b7e","last_hash":"3b7e","gap":false}
```

<!-- hook-example: audit_sink response -->
```json
{"version":1,"ack":"3b7e"}
```

### `secret_resolver`

Resolves a `secret_refs` item `NAME=hook:<plugin>/<name>#<key>` when `secret_resolve` or
`service_get` with `resolve_secrets` resolves the concept, outside the KB lock and after the core's
checks (`rw`, `secrets: none`, redaction unless `reveal`). See
[skills-services-secrets](skills-services-secrets.md) §Secrets held by a resolver hook.

| Request field | Meaning |
|---|---|
| `kb`, `concept_id` | the KB and the concept that declares the reference |
| `name`, `key` | the reference's `NAME` and its opaque key |
| `principal` | the caller, as for the veto |

| Response field | Meaning |
|---|---|
| `value` | the secret; required unless the answer denies |
| `ttl_seconds` | optional: cache the value for this long (at most 300 s and the entry's `cache_ttl`); absent, nothing is cached |
| `decision`, `reason` | `deny` refuses the resolution (`refused by the resolver: <reason>`) |

<!-- hook-example: secret_resolver request -->
```json
{"version":1,"hook":"acme/vault","request_id":"0123456789abcdef0123456789abcdef","time":"2026-01-01T10:00:00Z","kb":"work-kb","concept_id":"services/tickets","name":"API_TOKEN","key":"prod/tickets/api","principal":{"id":"user","kind":"human","via":"","groups":null}}
```

<!-- hook-example: secret_resolver response -->
```json
{"version":1,"value":"example-token-value","ttl_seconds":60}
```

### `identity`

Names the person behind a trusted proxy: asked only for a request on a `proxy: true` token from a
trusted peer whose user header carries an identity (`auth.proxy_identity`), never for a public path.
The answer is only names: the core maps the groups to roles and clamps them by the proxy token, so
the hook can never exceed it. Cached for `cache_ttl` (default 60 s). See
[transport-auth](transport-auth.md) §Identity and audit boundaries.

| Request field | Meaning |
|---|---|
| `peer` | the client address |
| `token_id` | the proxy token's id |
| `headers` | the request headers the entry lists (never `Authorization`) |
| `proxy_identity` | the `user` and `groups` the proxy's headers named |

| Response field | Meaning |
|---|---|
| `user`, `groups` | the identity that replaces the header one; no `user` is no opinion |
| `decision` | `deny` is a `403` (`identity denied`; a `reason` is not shown) |

Under `fail_closed` a failure is a `503` with `Retry-After: 30`; under `fail_open` the header
identity stays.

<!-- hook-example: identity request -->
```json
{"version":1,"hook":"acme/who","request_id":"0123456789abcdef0123456789abcdef","time":"2026-01-01T10:00:00Z","peer":"192.0.2.7","token_id":"sso","headers":{"X-Session":["abc"]},"proxy_identity":{"user":"user","groups":["staff"]}}
```

<!-- hook-example: identity response -->
```json
{"version":1,"user":"user","groups":["staff","ops"]}
```

## A hook-side plugin calling back

A hook that needs to call Meridian back (to read a page before it decides, say) uses a token bound
to its plugin (`plugin: <name>` in `auth.tokens`): its principal is forced to
`plugin:<name>/<id>`, so the audit and the commit trailer label what it does; it is a `client`
token, expiring, never admin nor proxy, and it gets no secrets, nor artifact writes unless a role
rule grants `artifacts: rw`. See [transport-auth](transport-auth.md) §A token bound to a plugin.

## How the plugin and the operator combine

A hook entry is active only when the operator wired it in the server config **and** its plugin
(the namespace of the hook name) is pinned and enabled in `plugins`; a missing plugin stops the
start. The manifest of a plugin does not yet declare which hook points it serves, so the point is
not cross-checked against it. Where and how the hook is called — the URL, the credential, the
timeout, the failure policy and the mode — is always the operator's. Removing every `hooks:` entry
restores the behaviour of a server without hooks exactly.
