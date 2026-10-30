---
topic: sync-provisioning
---

# D1408 — A client key, `kb_options.<kb>.artifacts`, stops one KB's artifacts without disconnecting it

**Decision.** `.cartographer.yaml` has a top-level map `kb_options: {<kb>: {artifacts: bool}}`, per KB and
valid for every provider on the machine; absent means `true`, today's behaviour. With `artifacts: false`
`sync` leaves the KB's skills, subagents, hooks and MCP descriptors out of the manifest, so the existing
lockfile-driven prune removes the ones installed, and rewrites the KB's generated instructions text without
its `Skills:` line. The KB stays connected: tools, routing sentence and the shared operational section stay.
The option is a narrowing that wins over every scope and binding and never adds a KB to any of them. It is
set with `cartographer client kb <kb> --artifacts=false` or in the YAML; the TUI edits no per-KB setting.

**Why.** Nothing said "this KB, without its artifacts": a workspace scope also changes which KBs a folder
reaches, and leaving a KB out of `clients.<provider>.kbs` drops its MCP entry too. Reusing the manifest as the
single lever means no new prune path: the only removal mechanism stays "the manifest no longer lists it".

**Alternatives rejected.** Dropping the KB's instructions artifact too: it would lose the routing sentence
(the `kb` argument rule) the connected KB needs. A per-provider axis: the need is about the KB, and a second
axis multiplies states. Filtering after the strict merge: an opted-out KB's skill would still collide with
another KB's and stop the sync. Re-signing the stripped instructions: the client holds no signing key.

**Consequences.** The server-built, signed instructions artifact is rewritten client-side by
`provisioning.WithoutSkillsLine`, after `VerifiedManifest`, with its hash recomputed; `Apply` trusts the
`Signed` flag. The drop of the other artifacts runs before the strict merge. The generated skills line must
stay the line right after the first blank line of the block (the round-trip test against
`generateKBInstructions` pins it). `KBOption` is the type later per-KB options extend. No lockfile or state
migration.
