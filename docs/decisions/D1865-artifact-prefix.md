---
topic: data-plane
---

# D1865 — A KB can require a scope prefix on its skills and agents

**Decision.** An optional `artifact_prefix` key in a KB's `settings.yaml` (the KB file, D746) makes every skill and agent whose name does not start with the value plus `-` an `artifact_unprefixed` lint finding, which the write gate refuses to introduce. The value is a slug without the separator (`[a-z0-9]+(-[a-z0-9]+)*`, at most 32 characters): `acme` requires `acme-review-mr`, and the bare `acme` lacks the prefix. Unset, nothing changes anywhere. `cartographer doctor` separately reports a KB agent or skill whose project-scope destination is a file a bound workspace tracks in git (`workspace-shadow`).

**Why.** A client chooses a skill by name and description before it reads the body, so a generic `review-mr` from a work KB is selectable in any session, and Kiro silently prefers a workspace agent of the same name over the global one. D171 refuses a collision between two KBs and nothing looked at a KB against a workspace. A scope prefix on everything a KB distributes, each description starting from the scope, is the remedy already used by hand. The hyphen is the one separator every client accepts in a skill name (the intersection rule, D191), and a value without it cannot be set to something a name can never carry.

**Alternatives rejected.**
- A separator inside the value (`acme-`): it admits separators a client may refuse, and the value could be set to something no name can carry.
- Checking hooks, MCP descriptors and instructions as well: a hook is fired by an event and lives under a per-hook directory, an MCP descriptor's name is a server key whose rename invalidates its hash-bound client approval (D115) and which D171 already refuses to collide, and instructions have no name. Only the two kinds a model selects by name and description, and a workspace can shadow by file name, are checked; a later kind is a deliberate step.
- A `kb_repair` fix kind: a rename changes the directory or file, the `name` field, the artifact's usage history (keyed by name, D1043) and every `requires` or prose reference, so it is a person's, like `artifact_unused`. The check is therefore not `AutoRepairSafe`.
- A line in the `sync` summary: the printer receives only the applied results, so it would cost a second git listing per workspace on every sync for a condition that does not change per run. Doctor only.
- Comparing by artifact name in doctor: the shadow is a file at the destination the project scope would write, so the comparison is by that path.

**Consequences.** "Committed" means tracked by git: an untracked file is the user's scratch or Cartographer's own projection, not a shadow. A KB that sets the key over existing unprefixed artifacts gets findings, not refusals (the gate compares before and after), while a write that introduces an unprefixed one is refused. A malformed value is a `settings_findings` entry and is ignored. The web bundle has no label for the new check yet and falls back to the check name.
