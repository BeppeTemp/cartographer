---
topic: control-plane
---

# D1303 — A malformed or unknown top-level tools/call is -32602; a sibling's refusal stays a result

**Decision.** A `tools/call` whose `arguments` is not an object, or that names a tool the server does not have, answers JSON-RPC `-32602` (invalid params) naming the tool, with no Go type in the text. The Atlas API takes `limit` only as a positive integer on every route that has one and answers 400 otherwise. The routed `kb "x": tool not found: <name>`, where a sibling KB has the tool and the named one does not (`routedmount.go`), stays an `isError` result.

**Why.** The MCP spec makes an unknown tool invalid params, and a Go unmarshal message tells a caller nothing. The routed refusal is a different fact: the tool exists on the connection and one KB declines it, as a denial does, so a caller that branches on `isError` for refusals keeps working.

**Alternatives rejected.** Keep the unknown tool as an `isError` result: contradicts the spec and hides the typo among tool failures. Make the routed refusal `-32602` too: the name is valid on the connection, so the call is not malformed. Clamp a non-positive Atlas `limit` to the default: `limit=-1` then answers 200 for a request that makes no sense.

**Consequences.** A client or script that greps an unknown-tool answer for `tool not found` must also look for a JSON-RPC `error` (`test/e2e` scenario 20 and the fake server in `cmd/cartographer/toolprefix_client_test.go` do). The unknown call is still audited as `unknown_tool`, and a pre-D288 prefixed name keeps its hint after `unknown tool <name>`.
