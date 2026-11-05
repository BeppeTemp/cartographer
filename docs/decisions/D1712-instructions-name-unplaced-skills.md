---
topic: sync-provisioning
---

# D1712 — The instructions block names the skills a client cannot install and how to read them through MCP

**Decision.** For a provider and scope with no skill cell, `UnplacedArtifacts` (computed by the client on the manifest before `ProjectManifest` drops them, passed in `ApplyOptions.Unplaced`) lists the KB's skills and the agents that are neither installed as subagents nor derivable into a skill. `applyInstructionsGroup` appends one paragraph per KB after the subagent sentence: the names (sorted, quoted and bounded like the other environment lines: `envQuote`, `envJoin`) and `artifact_read` with `path: skills/<name>/SKILL.md` (`agents/<name>.md` for agents), with the `kb` argument for a routed connection. Bundled artifacts are never listed. The text is client-side: it enters no artifact hash but is folded into the recorded block hash only when present, so a built-in keeps its hash and a changed skill set rewrites the block.

**Why.** A fork of an editor extension that takes skills only in a bound repository leaves the global scope with no file to put a skill in; the KB's skills would simply not exist for the agent. The skills are plain text readable through MCP, and the read works on an unbound folder (`access=read`), so the block points there. The rule is keyed on the missing cell, not on the provider: nothing in it knows what a variant is.

**Alternatives rejected.** A workflow that copies skills into some directory (a write outside any declared cell). Listing the skills in `kb_guide` only (the agent reads the block, not the guide, before it needs a skill). Deriving the agent set a second time here (it reuses the capability projection's own predicates).

**Consequences.** Both this sentence and `kb_guide` name the same tool and path shape (`provisioning.ArtifactReadShape`, pinned by a test in each package). In a projection that writes no instructions block (the global projection of a workspace scope) nothing is said: the limit is that the unbound folder then has no hint.
