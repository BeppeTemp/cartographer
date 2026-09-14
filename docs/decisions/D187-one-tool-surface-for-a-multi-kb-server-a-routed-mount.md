---
topic: transport-auth
---

# D187 — One tool surface for a multi-KB server: a routed mount with the KB as an argument

**Status: implemented.** Closes #242.

**Context.** A client that uses N Knowledge Bases paid N copies of the same tool schemas in its
fixed context, on **every** model round-trip. Measured against the running server (`tools/list` over
HTTP, one call per mount) with three KBs configured for the `kiro` provider:

```
work-kb       33 tools   27,392 bytes
notes-kb    33 tools   27,491 bytes
eng-team-kb     33 tools   27,458 bytes
                          ------------
                          82,341 bytes  ≈ 20,600 tokens
```

The three payloads are the same 33 tools, differing only by the [D102](D102-opt-in-per-kb-mcp-tool-name-prefix.md) prefix. On the session
analysed, 204 round-trips carried all three: the two KBs never called accounted for ~2.8 million
input tokens on their own, against 35,946 bytes for the entire rest of the fixed context (host
prompt plus steering).

Neither existing mitigation addresses it, because neither is about duplication. D65/D123's `agent`
profile shrinks the tool set **per mount**; D102's prefix makes multi-KB *work* on a flat-namespace
client. Multiply either by the number of mounts and the cost returns. The duplication is the
**topology**: `MultiKBServer` mounts one independent `Server` per KB, each registering the full tool
set, and the client writes one MCP entry per KB.

**Decisions.**

- **Opt-in, additive, default off.** `mcp.mount_mode: routed`
  (`CARTOGRAPHER_MCP_MOUNT_MODE`, `--mount-mode`) adds `/mcp/routed` alongside the per-KB endpoints.
  `?kb=` and `/mcp/<name>` keep byte-identical `tools/list` output and identical dispatch — pinned by
  a test that compares the per-KB payload with and without routing enabled. Nothing changes for an
  existing deployment until the key is set.
- **The KB is an explicit tool argument, never inferred.** Every tool's `InputSchema` on the routed
  mount carries a `kb` property, required whenever 2+ KBs are routed; a missing `kb` is an error
  naming them. With exactly one KB routed it is optional — there is no ambiguity to resolve. A
  default KB would land a write in the wrong archive on a model slip, which is precisely the failure
  D102's flat-namespace warning exists to prevent; it must not return as a convenience.
- **The exposed set is the union, refused per KB at dispatch.** An intersection would silently hide
  `artifact_write` from a KB that allows it because a sibling does not. The union registers it once;
  a call naming a KB that gates it off gets an error carrying the tool, the KB and the config key,
  reusing `unknownToolMessage`'s existing setting lookup.
- **Everything per-KB is resolved after `kb`, by dispatching into the per-KB server's own
  `callTool`.** That is the whole of what a per-KB endpoint does with a call — the read/write
  classification, the D118 authorization policy, the audit pair naming the real KB, the git lock and
  commit wrapper — so the routed path cannot drift from the per-KB one. The routed `Server` carries
  no audit log and an authorizer that defers every tool decision, so exactly one authorization and
  one audit record happen, at the target.
- **Metadata is authorized as "can reach at least one routed KB".** The fail-closed metadata gate
  still has to answer for `initialize`/`tools/list`, and on a routed mount the honest generalization
  is that a principal scoped to one KB legitimately lists the union and is refused per call on the
  others.
- **The `kb` property is injected at one point.** The routed mount decodes each tool's schema and
  adds the property when it assembles its descriptor list — not by editing fifty schema literals,
  which would drift the moment a tool is added.
- **No tool-name prefix on a routed KB, and no KB named `routed`.** Prefixes disambiguate N mounts
  on a flat namespace; with one mount there is nothing to disambiguate and a prefix would only
  re-inflate the names this mount exists to shrink. The two requests are distinguished by who made
  them: the **derived** prefix of D153's `kb-name` default is simply **not applied** under routing —
  the operator never asked for it, and failing startup over a default nobody set would make the mode
  unusable out of the box (found by the E2E scenario, which is what it is for). An **explicit**
  `kbs[].tool_prefix` is a **fatal config error** naming the KB and the key: that one was asked for,
  and it contradicts the request to route. A KB named `routed` would collide with the endpoint's own
  path and is refused for the same reason. With routing off, `/mcp/routed` falls through to
  `/mcp/<name>`, so such a KB keeps its endpoint.
- **One channel for the choice.** A `?kb=` on `/mcp/routed` is `400`, the same rule `/mcp/<name>`
  already applies to a conflicting `?kb=`: two channels for one choice are how they get to disagree.
- **The client detects routing from `/health`, never assumes it.** The server emits
  `mount_mode: "routed"` and `routed_path`, and omits both otherwise — which is exactly what a
  pre-D187 server and a `per-kb` server look like to any client. `connect`/`sync` write **one** MCP
  entry against a routed server, persist the fact in `.cartographer.yaml`
  (`server_mount_mode`, `server_routed_path`), and `doctor` derives the expected entries from it
  offline. The per-provider KB binding (D169/D170) is untouched: routing changes the transport, not
  the authorization.
- **The generated instructions name the tools as the agent will see them.** This is the part the
  model actually reads, so a wrong name there would be worse than the duplication being removed. On
  a routed server the block names the bare tools — a routed mount refuses a prefix — and states the
  `kb` value to pass for that KB. The fact reaches `BuildManifest` through `Deps.RoutedMount`, the
  same way D144 plumbed the prefix.
- **A mode switch is a reconnect, reported and not healed.** It changes the *shape* of every entry,
  which an incremental sync cannot see. `status` prints `mount mode changed: …` and names
  `cartographer reconnect`, consistent with how D142 handles a server-version change. The removal
  set already covers both shapes, so no orphan survives.
- **The flat-namespace warning is silent against a routed server.** One entry cannot collide with
  itself.

**Invariants kept.** The per-KB endpoints' `tools/list` is byte-identical with routing on or off;
`TestServer_ToolsProfile` keeps pinning the agent-visible set for a single-KB mount; audit records
name the KB actually operated on, not the mount; `SetDisplayName`'s `cartographer:<kb>` handling for
2+ per-KB mounts is unchanged.

**Consequences.** Measured by the E2E scenario against a real three-KB server, `tools/list` goes
from 81,633 bytes across three mounts to 31,969 on the routed one (115,932 → 46,196 on the Go test
fixture, whose KBs register more tools) — the `kb` property makes each schema marginally
larger, which is why the assertion is "well under two copies" rather than "exactly a third". New
opt-in mount mode, no default change; minor bump.

Details: `docs/transport-auth.md` §Mount modes, `docs/deployment.md` §HTTP routing,
`docs/control-plane.md` §MCP API, `docs/configurator.md` §Routed servers.
