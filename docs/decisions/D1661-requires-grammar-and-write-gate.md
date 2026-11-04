---
topic: sync-provisioning
---

# D1661 — `requires` is one grammar, guarded at the write gate because the hook decoder is lenient

**Decision.** A skill, an agent or a hook may declare `requires`: a list of entries, each `<kind>:<name>` (kind
`skill`, `agent` or `hook`) or a capability id of the table of D1656. Carriers: the `requires` frontmatter key of
`SKILL.md` and of `agents/<name>.md`, and the `requires` array of `hook.json`. `provisioning.ValidateRequires` is the
single validator; it is called by `artifact_write` for the three carriers and by `ValidateHookJSON`, so lint's hook
check sees the same error. It refuses an empty, duplicate, negated (`!`), self-referring, `mcp:` or `instructions:`
entry, an unknown kind, an invalid artifact name, an unknown capability id and a list over 32 entries.
`instructions.md` refuses the key by name; an `mcp` descriptor already refuses it (unknown field).

**Why.** A capability id is checked against the id set, never its status: the status is per client and changes with
probing, so a write-time check on it would reject a valid declaration. A reference to an artifact that exists nowhere in
the KB is accepted, because it may come from another KB bound to the same client. `hook.json` has no frontmatter and
`parseHookSpec` is lenient on purpose (a sync never stops registering a hook over an unknown field), so `requires` is
read from it separately and `hookSpec` does not carry it: a wrong type must not stop a hook from registering, and the
write gate and lint are the only guards on that carrier.

**Client side.** `RequiresOf` reads the requirements from the source files, as `ArtifactRequiresApproval` does; nothing
is added to the wire or to the `Artifact` JSON. A source that cannot be parsed is an error the caller reports as a
warning and treats as "no requirements". No client acts on `requires` yet: evaluation is a later slice, and only a
`verified` capability will satisfy a capability entry (D1656).
