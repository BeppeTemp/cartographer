---
topic: skills-services-secrets
---

# D212 — A skill's frontmatter is validated with the parser a client uses, not with ours

**Decision.** `skill.Validate` — the single authority both write channels already
call (D191) — gains two error-severity rules:

| Rule | Fires when |
| --- | --- |
| `frontmatter_unparseable` | the OKF reader could not read the block, with its reason |
| `frontmatter_invalid_yaml` | the OKF reader could read it but `gopkg.in/yaml.v3` rejects it |

`Skill` therefore carries the **raw frontmatter block** and the reader's error, so
the validator can judge the frontmatter itself rather than only the fields
extracted from it. `internal/okf` is **not** made stricter, and concept
frontmatter is not touched.

**Why.** `internal/okf` is a lenient, stdlib-only reader (D8). Given

```yaml
description: Sibling of the plan-issue skill: it writes the issues
```

— an unquoted plain scalar containing `": "`, which the YAML spec forbids — it
splits on the first colon and produces exactly the string the author meant. Every
agent client parses the same file with a spec-compliant parser, gets
`mapping values are not allowed in this context`, and **drops the skill from its
catalogue with no message anywhere**: not an error, not a log line, not a degraded
mode. The skill simply does not exist for that session.

This was measured on this repository's own `implement-issue` skill, which had
shipped that way. Three readers here called the file valid — `okf.ParseFrontmatter`,
`skill.Validate`, and a hand-rolled frontmatter splitter in the documentation
gates — while `yaml.v3` rejected it outright, and a client listed thirty-one skills
for the workspace and not that one.

So the lenient reader is the *more* useful one for extracting fields, and it is
structurally unable to answer the only question that matters for an artifact
handed to someone else's parser: **will a client load this?** That question needs
the client's parser, which is why the check is a second read rather than a change
to the first.

**Where it belongs.** `skill.Validate`, and nowhere else. D191 already made it the
one authority, called by `artifact_write` before the write and by `BuildManifest`
for a skill that arrived through git, and made an error-severity issue *exclude*
that skill from the manifest with the KB, the skill and the rule named. Adding a
rule there therefore closes both channels at once and gives the reporting for free:
the write is refused with the reason, the sync excludes the artifact instead of
publishing something the client will discard, and the KB's other skills still ship.

**Alternatives rejected.**

- *Make `internal/okf` strict.* It is the reader for every concept in every KB, so
  tightening it is a behaviour change for all existing content — with no migration
  path and no way to know in advance what would start failing. And it would be the
  wrong trade even if it were free: leniency is what lets a KB's frontmatter be
  written by hand.
- *Extend the check to concept frontmatter.* Deliberately not done, and the
  asymmetry is the reasoning: a concept is read by Cartographer alone, so our
  reader *is* the consumer and its verdict is the only one that matters. A skill is
  handed to a third-party parser, which makes that parser the authority. Same file
  format, different consumers, different rules.
- *A lint check instead.* `lint` does not look at skills at all today, so this
  would have meant a new lint domain to report something the write channels can
  simply refuse. Refusing at the boundary beats reporting after the fact, and the
  exclusion diagnostic already reaches the operator through `sync`.
- *Warn instead of refusing.* A warning does not exclude the artifact (D191), so the
  unloadable skill would still be published to every client and still be dropped
  there. The whole defect is that nothing downstream complains.
- *Check it only in this repository's own gates*, which is where it was first
  caught. That protects two files and leaves every KB exposed.

**Consequences.** `LoadSkill` and `LoadAllFromFS` no longer discard the reader's
error: it used to produce a `Skill` with an empty `Name` and `Description` and no
error to the caller, so the reason surfaced later as `name is required` — true, and
about the wrong thing. A `Skill` assembled in memory carries no raw frontmatter and
gets no frontmatter check, which is the right default for a value that was never
parsed from a file. The bundled skills are covered by a test of their own, since
they are the ones a fresh install hands to a client. An existing KB with such a
skill will see it excluded from the manifest on the next sync, with the rule named:
that is a visible change, and the skill was already not working.
