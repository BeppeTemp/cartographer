---
topic: control-plane
---

# D1279 — A tool argument the schema does not declare is JSON-RPC -32602, checked after authorization

**Decision.** A `tools/call` carrying a top-level argument that is not in the tool's `inputSchema` answers JSON-RPC `-32602` (invalid params) naming the unknown key(s). The check runs after the unknown-tool answer, the argument-shape check and authorization, and the refusal is audited as a denial before it is answered. Only the top level is checked; the routed `kb` and the arguments a tool lists in `Tool.HiddenArguments` (accepted, kept out of `tools/list`, such as `map_create`'s `map` alias and the removed `search` arguments that have their own message) are accepted. Messages the HTTP transport cannot take as a request answer JSON-RPC bodies (`-32700`, `-32600`, `-32601`), like a refused Origin.

**Why.** A typo such as `if_mach` was ignored, so the optimistic-concurrency check it asked for silently did not happen. Doing the check after authorization keeps the denial the answer a caller who may not use the tool gets, leaves a record of the refusal (D119, D132) and does not let a caller without access probe a tool's schema. It costs a Go error out of the SDK handler on this one path, the only one: every other outcome stays a result with `isError`.

**Alternatives rejected.**
- Ignoring unknown keys (the previous behaviour): a typo drops the check silently.
- A warning in the result: clients do not read it, and the write has already happened.
- Checking before authorization: the denial would go unaudited and an unauthorized caller could map the schema.
- Declaring hidden aliases in the schema: every `tools/list` pays for them (D285).

**Consequences.** A handler that reads a key its schema does not declare must list it in `HiddenArguments`; `TestHandlerArgumentsAreDeclaredOrHidden` fails otherwise. A client that sent undeclared keys now gets an error: this is a behaviour change, noted in the PR.
