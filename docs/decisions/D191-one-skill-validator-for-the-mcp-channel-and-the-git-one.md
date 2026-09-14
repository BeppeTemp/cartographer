---
topic: skills-services-secrets
---

# D191 — One skill validator, for the MCP channel and the git one

**Status: implemented.** Closes #245.

**Context.** A skill written through MCP and the same skill arriving through git were validated by
two different rules, and the git channel was the weaker one. A malformed artifact it accepted broke
the sync of the **whole KB** later, far from its cause.

Two gaps.

**The shared validator was too thin.** `skill.Validate` checked only a non-empty name, a non-empty
description, and a body over 500 lines as a warning. It did not check kebab-case, a maximum name
length, or that the frontmatter `name` matched the directory. The clients do impose those rules —
Kiro and OpenCode document lowercase kebab-case, 64 characters maximum, equal to the directory, and
OpenCode additionally rejects consecutive `--`. A skill that passed our validation and was refused
by the client was a silent no-op in the agent's catalogue, invisible from here.

**The two channels did not run the same check.** `artifact_write` called `validateSkillArtifact`,
which required `name` to equal the directory. `BuildManifest` — which serves the git channel and is
declared equivalent — called `skill.LoadAllSkills` and **discarded the error slice**
(`kbSkills, _ :=`), never invoking `Validate` at all. With directory `skills/foo/` and frontmatter
`name: bar`, the manifest registered the artifact as `bar` but hashed the directory `foo`;
`ReadArtifactFiles` then looked for `skills/bar`, the read failed, and it could take `sync_pull`
down for the entire KB. A hand-edited or git-imported skill broke every client of that KB, while
identical content would have been refused outright over MCP.

**Decision.**

- **One validator, called from both channels.** `skill.Validate` is the single authority.
  `validateSkillArtifact` delegates to it and keeps only the MCP-specific part (the path shape of
  the incoming write); `BuildManifest` calls it too.
- **The rule set is the intersection of what the clients accept**, not the most permissive union:
  lowercase `[a-z0-9]` segments separated by single `-`, no leading/trailing `-`, no `--`, at most
  64 characters, and frontmatter `name` equal to the directory. Adopting the strictest client rule
  is the point — the failure being fixed is a client silently ignoring a skill.
- **Severity split.** A name/directory mismatch, an invalid or over-long name and an empty
  description are **errors**: they break a channel or a client. Body length and description length
  stay **warnings** — they degrade quality without breaking anything, and turning an existing KB's
  long descriptions into hard failures would block syncs that work today.
- **`BuildManifest` reports, it does not silently skip.** An invalid skill is excluded from the
  manifest and the reason is surfaced with the KB, the skill and the violated rule named — through
  a `SkillDiagnostic` callback (the same shape `MCPDiagnostic` already had, wired to the server's
  startup log) and on `Manifest.Issues` for a programmatic caller. The errors `LoadAllSkills`
  returns are surfaced the same way instead of being discarded.
- **`Issue` carries the rule it violated**, so a caller attributes a refusal without parsing a
  message.
- **The `<namespace>--<skill-name>` convention is dropped.** It was unreachable from production
  code and incompatible with the `--` rule above. The comment, the `TestNamespaceExtraction` test
  exercising a local `strings.SplitN` with no production caller, and the E2E fixture
  `kbinfra--query-rete` (now `query-rete`) are all retired. Namespacing, if it is ever needed again,
  comes back as a separate decision rather than as a dormant string convention.

**Invariants kept.** A KB with one invalid skill still syncs its valid artifacts — the failure is
scoped to the offending skill, the opposite of the whole-KB `sync_pull` breakage it replaces.
`LoadSkill` keeps its signature and parsing. `Manifest.Issues` is deliberately **not** part of the
revision: excluding an artifact already changes it, and hashing the message list would make an
edit to the wording look like a catalogue change.

**Consequences.** Skills that pass today can be refused after this change — breaking for KBs whose
skill names are non-conforming, and the release notes must name the rules. `docs/sync.md` and this
page also stop contradicting each other on `signed`: the signature and the trust policy are now
described as the two distinct things they are.
