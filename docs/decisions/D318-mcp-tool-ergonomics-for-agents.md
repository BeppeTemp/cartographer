---
topic: control-plane
---

# D318 — The tools an agent is told to call are listed, and their answers parse

**Decision.** `concept_batch`, `asset_delete` and `artifact_delete` (registered
only under `allow_artifact_write`) leave `advancedToolNames`: an agent-profile
client sees them in `tools/list`. The responses an agent reads are made regular:
`lint` on a clean scope returns the JSON shape of a dirty one; `kb_repair` with
`dry_run: false` drops the `planned` array; `kb_review` reports `limit_applied`
and, when it clamped the request, `limit_capped` and `limit_max`; `gate_check`
accepts an empty `changed_ids` as the whole-KB gate (no commit gate);
`asset_delete` without `if_match` says so instead of "stale write". A `patch`
operation of `concept_batch` counts the edits it carries toward the 512 KiB
aggregate, not the body that results. The gap between `kb_status.conformance`
and `lint` counts is documented, not closed.

**Why.** The bundled `kb-doctor` skill prescribes `concept_batch`, and a
descriptor-bound client cannot call by name what `tools/list` omits (D123): the
skill and the profile contradicted each other. The other changes remove a
parse failure (a bare string where JSON is expected), a silent truncation, a
refusal with no use case, and a misleading error. The batch limit bounds the
request line, so counting the post-patch body charged an agent for bytes it
never sent.

**Alternatives rejected.**
- Keep `concept_batch` unlisted and fix the skill: the skill is right, a
  multi-page refactor is a normal doctor session.
- Make `conformance.findings` equal to the lint total: the subset is the
  structural debt the doctor can act on (D290); only the explanation was missing.
- Server-side unescaping of `old_string`: the matching is a plain substring and
  correct; the fragility is the client's JSON escaping.
- Hold the D285 budget at 23 KiB: after cutting `concept_batch` from 3 KiB to
  1.4 KiB (the field detail moved to `docs/control-plane.md`) the catalogue was
  1.8 KiB over, and the other descriptions were at their floor. The budget is
  raised to 25 KiB; `artifact_delete` (~0.3 KiB) is outside it, since it only
  appears with the per-KB flag.

**Consequences.** Callers that parsed `lint`'s "Lint OK" string, or read
`planned` after an apply, break; both were brittle or redundant. Every further
tool in the agent profile is paid for against 25 KiB. `kb_review`'s
`lint_judgement` items are `lint` findings under another name: their totals must
not be added.
