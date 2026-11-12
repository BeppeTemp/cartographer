---
topic: control-plane
---

# D1994 — `ingest_setup` is in the agent tools/list

**Decision.** `ingest_setup` leaves `advancedToolNames`: an agent-profile client sees it in
`tools/list`. The bundled `cartographer-kb-ingest-plan` skill no longer says it is served only by
name; it keeps the check that a server with no `ingest_setup` at all predates the ingest data model.

**Why.** The planner skill prescribes `ingest_setup` as step 1 of a campaign, and a descriptor-bound
client (one that builds its tool list from `tools/list`) cannot call by name what the list omits:
on such a client the campaign could not start. It is the defect D318 fixed for `concept_batch` and
D1191 for the secrets tools. The cost is one more tool in the agent's default working set, for a
call that is rare and idempotent.

**Alternatives rejected.**
- Keep it advanced and tell the operator to run it: the skill would stop on every descriptor-bound
  client, the clients it is most used from.
- Move template provisioning into `map_create`: a declaration makes a map strict, and D1201 left the
  choice of the Maps to the planner.

**Consequences.** A bundled skill's `- Tools:` reference bullet names only tools `tools/list`
advertises, unless the line says the tool is not in `tools/list`
(`TestBundledSkillToolListsAreAdvertised`). Making a tool a skill prescribes advanced again breaks
that skill on descriptor-bound clients. Amends D1256 §Consequences: the planner no longer carries
a pointer for clients that cannot call `ingest_setup`.
