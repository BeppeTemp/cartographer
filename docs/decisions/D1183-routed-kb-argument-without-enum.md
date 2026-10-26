---
topic: transport-auth
---

# D1183 — The routed `kb` argument is a required string, not an enum

**Decision.** On a routed mount with two or more KBs every tool carries `kb` as a required string with a fixed short description pointing at the KB list of the instructions, and no enum. Validation stays server-side: a missing or foreign `kb` is refused with the allowed names. With one KB the property stays absent. Amends D288, which promised an enum.

**Why.** The enum and its long description made `tools/list` grow with tools times KBs (about +6.7k tokens with 9 KBs; on the test mount 64 tools went from 21 KB to 32 KB at 3 KBs). Clients that send every schema on every call pay it on each model call. The names already reach the model through the operating guide (`initialize.instructions`, or the managed block for a provisioned connection).

**Alternatives rejected.** Dropping `required` as well: would reach the "few hundred bytes over the single-KB list" target, but the maintainer chose to keep the contract explicit. A per-set description naming the KBs: still grows with KBs.

**Consequences.** The size of the list no longer depends on the number of KBs (pinned by `TestRoutedToolsListDoesNotGrowWithKBs`); the overhead over the single-KB list is the fixed per-tool cost of the property, measured at 6768 bytes over 64 tools in the indented JSON of today (about 105 bytes per tool). The "few hundred bytes" target is not reachable while `kb` stays required. A client that validated arguments against the enum no longer rejects a stale name locally: the server error names the allowed KBs. The instructions' KB list must keep one routing sentence per KB of the connection.
