---
topic: sync-provisioning
---

# D1656 — A per-client capability table, tied to the harness ledger by a test

**Decision.** `internal/provisioning/capabilities.go` holds, per provider and scope, a status (`verified`,
`unverified`, `absent`) for `skill`, `mcp`, `agent.delegate`, `agent.invoke` and `hook.<Event>`. The destination
matrix stays the source of truth for where a kind installs; the table says whether it works. `absent` is derived
(no destination in that scope, or an event outside `hookEventReach` for that provider); every other cell starts
`unverified` and only a probe makes it `verified`. `AgentMode` is the one place that decides whether an agent is
native (its cell exists and `agent.delegate` or `agent.invoke` is not absent) or degradable. The ledger is a
fenced `capabilities` block in each provider section of `docs/harnesses.md`, and `TestCapabilityTableMatchesLedger`
fails on any difference with the Go table.

**Why.** A destination says an agent or skill is written, not that the client uses it. Later slices degrade
agents to skills and skip artifacts whose requirements are unmet; both need one declared answer per client,
checked against evidence, rather than guesses spread over the code.

**Alternatives rejected.** Deriving everything from the destination matrix: it cannot say "installed but not
delegated to". Keeping the ledger as prose only: it drifts, as D207 found for the matrix. A single global
status per client: the project and global scopes differ (Cline and Pi have no project cells).

**Consequences.** A PR that flips a destination or a hook reach changes the table's ledger block in the same
change. A client whose cells were never probed ships `unverified`, which keeps today's installation behaviour.
Nothing consumes the table in this slice.
