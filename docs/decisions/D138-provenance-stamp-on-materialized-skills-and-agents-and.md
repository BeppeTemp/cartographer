---
topic: sync-provisioning
---

# D138 — Provenance stamp on materialized skills and agents, and two hashes per managed file

**Decision.** A materialized `skill` (its `SKILL.md` only) and `agent` carry a marker-delimited
provenance block appended to the file, following the existing
`<!-- cartographer:provenance:begin … -->` / `:end` convention with the begin marker matched by
prefix. It names the source KB, the artifact's path in that KB, the artifact's content hash, and
the one remediation an agent can act on: local edits are replaced on the next sync, and the change
belongs in `artifact_write` on that KB at that path. Bundled artifacts are stamped too, stating the
Cartographer bundle as their source and offering no `artifact_write` instruction. Hooks, `mcp`
descriptors and `instructions` are never stamped. The block carries no timestamp and no manifest
revision, is rebuilt from the source content on every materialization (so re-stamping is a fixed
point and an older block is replaced, never nested), and is applied client-side only — the KB's copy
is untouched, like placeholder expansion.

`ManagedFile` gains `materialized_hash`: the hash of what was actually written, after expansion,
stamping and per-provider translation. `content_hash` keeps meaning "the manifest artifact's hash",
which is what `ComputeDiff` compares. An empty `materialized_hash` means unknown (any lockfile
written before this change) and is never a mismatch.

**Rationale.** A materialized skill was indistinguishable from a hand-written one: every other
managed artifact announces itself — the instructions block, the generated OpenCode plugin, the Codex
MCP block, the bootstrap script — but the two kinds an agent actually *reads* did not. So an agent
improving a skill had no address to send the improvement to, and no warning that its edit was about
to be overwritten; with several clients on one KB the round trip was undiagnosable from the file
itself. Naming the tool, the KB and the path inside the file is what makes the supported channel
reachable without documentation the agent may not have.

Separating the two hashes was a prerequisite, and fixes a latent defect on its own:
`copyArtifactFiles` returned the *expanded* hash and Apply stored it in `content_hash`, which
`ComputeDiff` then compared against the *manifest* hash — so any artifact containing a
`{{repo:…}}`/`{{path:…}}` placeholder compared unequal on every sync, was reported `Updated`,
rewritten, and showed as permanent drift in `cartographer status`. Artifacts without placeholders
were unaffected, which is why it went unnoticed; stamping would have made every skill and agent hit
that path, turning a corner case into the default.
