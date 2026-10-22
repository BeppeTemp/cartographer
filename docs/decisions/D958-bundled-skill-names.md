---
topic: skills-services-secrets
---

# D958 — The bundled skills take the `cartographer-` prefix, and the old names keep working

**Decision.** The five bundled skills are `cartographer-kb-conflict-resolve`,
`cartographer-kb-create`, `cartographer-kb-doctor`, `cartographer-kb-import` and
`cartographer-kb-ingest`; `cartographer-ops` is unchanged. One map in
`internal/skillbundle` knows the old names. `skill_install` accepts an old name, installs
under the new one and says so (`renamed_from`). A KB copy under an old name is the
`legacy_skill_name` lint finding, and `kb_repair` moves it to the new name in one commit,
keeping the body. `cartographer sync` warns once per KB while such a copy exists.

**Why.** The bundled skills reach every user's agent catalogue next to other projects'
skills, and five of the six did not say whose they are (D957 did the same for the
repository's own skills and left these as a separate, breaking plan). The cost is that a KB
which installed `kb-create` holds a copy the client now receives beside the bundled skill,
under a second name, with instructions that drift apart.

**Alternatives rejected.** Keeping the old names as aliases in the bundle: two names for
one skill forever, and the catalogue shows both. Moving a KB copy silently on upgrade:
nothing in this project rewrites a KB without an explicit call (D290). Dropping the old
names from `skill_install`: every agent and script that names `kb-create` would get
"unknown skill" with no hint of the rename.

**Consequences.** A name is never removed from the legacy map, since a KB copy made years
ago still carries it. When both copies exist the repair reports a conflict and leaves them:
two diverged skills are a person's merge. The `kb-doctor` literal that `kb_status` matches in
`log.md` is a log marker, not the skill name, and stays, because existing KB logs carry it
(`internal/mcpserver/conformance.go`). Old decisions and `CHANGELOG.md` keep the old names:
they are records. The release notes name the five renames.
