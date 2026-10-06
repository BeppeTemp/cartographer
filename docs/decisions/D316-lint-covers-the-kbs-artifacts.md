---
topic: control-plane
---

# D316 — Lint covers the KB's artifacts, not only its concepts

**Decision.** A whole-KB `lint` checks what a KB ships beside its concepts: skills
(`skill_invalid`, `skill_warning` from `skill.Validate`; `skill_broken_ref`;
`skill_git_command`; `skill_missing_perimeter`), every artifact text file
(`legacy_tool_name`, `cross_kb_path`), the KB itself (`missing_instructions`), the
junk files git tracks (`junk_file`, and `junk_asset` in place of `orphan_asset`),
and sops pipelines in skills and concepts (`sops_format_mismatch`,
`sops_missing_file`). Two optional `instructions.md` frontmatter keys feed it:
`perimeter` and `legacy_paths`. `legacy_path` and `legacy_tool_name` carry mechanical
fixes (`replace_prefix`, `strip_tool_prefix`) that `kb_repair` applies. `kb create`
writes `data/.gitignore` with the shared junk patterns, the Atlas Artifacts panel
shows the findings per artifact, and a materialized KB skill gets a generated
`SOURCE.env` naming its KB.

**Why.** Lint saw concepts and hooks (D284) and nothing else, so a skill that no
client could catalogue, a skill calling pre-D288 tool names, or a `.pyc` committed by
an agent stayed invisible until something broke on a client. `orphan_asset`'s one
advice ("cite it") was followed literally on a `.pyc`. Every check is a regex or a
`stat`, warning or info, so no gate turns red on an existing KB; the cost is false
positives, which is why code is scanned and prose is not, the cross-KB roots are
injected rather than guessed, and the perimeter and legacy-path checks only run when
the KB declares the key.

**Alternatives rejected.**
- A separate `artifact_lint` tool: two lint surfaces, two caches, and the Observatory,
  `kb_status` and the gate would each need to learn it; one lint with artifact paths
  marked as such (`Finding.Artifact`) costs one field.
- `Run` unchanged and only the `lint` tool passing sibling roots (`RunWithOptions`):
  the cached findings behind `kb_status`, the Atlas and the gate would disagree with
  the `lint` tool on the same KB. The roots live on `kb.KB.SiblingRoots`, set at
  mount; `RunWithOptions` stays for an explicit caller.
- Run the concept-side sops and legacy-path checks in the unscoped block: they would
  ignore scope and `lint_ignore`, which the plan promised them; they run per concept.
- One `legacy_path` finding per concept with every prefix in its message: a `Fix` has
  one `field`/`to`, so the repair would have to re-read `instructions.md`. One finding
  per declared prefix found (never per occurrence) keeps the fix self-contained; the
  repair groups a concept's prefixes and applies them in one pass, longest first.
- Resolve `skill_broken_ref` paths against the KB root only: `scripts/run.sh` in a
  skill conventionally means the skill's own directory, so both are tried.
- Scan Python relative imports for broken references: a relative import resolves
  against the importing script, which a `SKILL.md` body does not name.
- `CARTOGRAPHER_KB_ROOT` exported by the bootstrap hook: Claude-only
  (`CLAUDE_ENV_FILE`); a file beside the skill works for every client. Exported by
  the service: a user daemon cannot inject variables into client processes.
- `CARTOGRAPHER_KB_ROOT` always: a client fed over HTTP has no copy of the KB, and the
  server's path would point at nothing on it; the line is written only when the root
  is on the materializing machine.
- A root `.gitignore`: D62 removed it and stands; `data/.gitignore` travels with the
  content, written only when `kb create` creates the KB so an existing KB gets no
  untracked file.
- `kb_repair legacy_tool_name` without `allow_artifact_write`: it would be a second
  way to write a skill that the per-KB opt-in (D71) does not gate.

**Consequences.** A caller that maps a finding path back to a concept must honour
`Finding.Artifact` (`findingConcept` in the UI API, `planRepair`, `lint.Review`);
an artifact finding is shown only to a whole-KB reader. `kb.JunkPatterns` is the one
junk list: `kb create`, `junk_file`, `junk_asset` and the `orphan_asset` exclusion
read it. `SOURCE.env` is a generated file like hermes' `SOURCE.md`: part of the
skill's materialized hash and lockfile entry, and a KB skill shipping a file of that
name is not materialized; a skill materialized earlier gets it on its next change.
The `legacyToolNameRe` relies on no current tool name containing `__`, and the
cross-KB match on a root boundary, so `kb` never matches `kb-b`.
