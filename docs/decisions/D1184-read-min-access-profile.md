---
topic: control-plane
---

# D1184 — `?access=read-min`: a minimal read-only tool profile

**Decision.** `/mcp/routed?access=read-min` is a second value of the existing `access` parameter. The connection is read-only and `tools/list` carries exactly nine tools, in this order: `search`, `concept_read`, `index_get`, `graph_context`, `graph_neighbors`, `concept_list`, `atlas_overview`, `artifact_read`, `kb_guide`. The listing is narrowed, calling is not: every other read tool stays callable by name, as the advanced tools do (D65); every write tool is refused at the routed authorizer with the new code `read_only_profile`, whose hint names no CLI command. The header is `Cartographer-Access: read-min`. The client side is `connect --access read-min`, saved in `clients.<provider>.access` (not a KB binding), written into the entry URL after `kbs=` and before `provisioned=1`.

**Why.** An agent that only reads pays the schemas on every model call (clients such as Kiro, Strands or Bedrock agents send all of them each time): measured 4.9k tokens for `access=read` (25 tools) against 2.0k for these nine. A new value of `access`, not a new `tools=` parameter, because `access` already says what the connection may do; an old server answers 400 to the unknown value, so a client never silently gets the full profile and needs no version gate.

**Alternatives rejected.** Reusing the `access=read` mode as is: its refusal (`unbound_folder`) and sentence tell the agent to run `cartographer workspace bind`, which is false for a deliberate read-only connection and forbidden for a generic client (D1101). The new mode has its own refusal, sentence and cache key. Support on `/mcp/<name>` and stdio: out of scope, as for `access=read`. Carrying the profile through the workspace bridge: refused at `connect` with the provider named.

**Consequences.** `readMinTools` must stay inside `readOnlyToolNames` (`TestReadMinToolsAreReadOnly`); the list is pinned by a golden file (`testdata/tools_list_read_min.json`, `-args -update`) and a byte budget (`toolsListReadMinBudgetBytes`, D285). A write refusal does not depend on the token: a read-write token is refused too. A sync of a provider that later gains a workspace binding writes the bridge entry without the profile; `connect` refuses that combination up front.
