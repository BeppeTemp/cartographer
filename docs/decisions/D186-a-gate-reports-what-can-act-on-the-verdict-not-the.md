---
topic: control-plane
---

# D186 — A gate reports what can act on the verdict, not the whole archive

**Status: implemented.** Closes #241.

**Context.** Measured on a real Kiro session against `work-kb` (715 concepts): a single
`gate_check` call returned **28,235 bytes of `lint_findings` — 101 findings — while reporting
`pass: true`**. The breakdown: `concept_oversize` 55, `broken_link` 19, `machine_path` 13,
`index_incomplete` 6, `map_oversize` 5, `secrets_on_non_service` 2, `orphan_asset` 1; by severity
**61 info, 40 warning, 0 error**. It was called four times in that session.

Two distinct defects sat behind that number.

**Findings that cannot fail the gate were serialized anyway.** `toolGateCheck` computed `pass` from
`lint.SevError` only, then serialized *every* finding regardless of severity. The `info` checks —
`concept_oversize`, `map_oversize`, `orphan_asset`, `contract_malformed` — are by construction
incapable of blocking anything, and in the measured response they were 60 of 101 findings and the
bulk of the bytes, because `concept_oversize`'s message embeds the path plus both thresholds.

**The scope was hardcoded to the entire KB.** `k.Validate("")` and `lint.Run(k, "", false)` ignored
scoping entirely, while `changed_ids` — the only input the schema accepted — was used for the commit
gate alone. Gating a two-concept change paid a full-archive lint. The standalone `lint` tool already
had `scope`/`scope_neighbors`; `gate_check` did not.

**Decision.**

- **`gate_check`'s default floor is `warning`; `lint`'s stays `info`.** The two tools are called for
  different reasons: a gate is asked for a verdict and the evidence behind it, `lint` is asked to
  *show* findings. Changing an existing tool's default response is deliberate here — the current
  default is the pathological one.
- **A count always survives the filter.** Omitting findings silently would make the response lie
  about the state of the KB. Every response carries `counts_by_check` and `counts_by_severity`
  computed **before** filtering, plus `findings_omitted`, so a caller always knows the shape of what
  it is not being shown. `lint`'s `count` keeps meaning the unfiltered total, which is what it has
  always meant.
- **`gate_check` gets an explicit `scope`, not a scope derived from `changed_ids`.** Deriving one
  would silently narrow a gate that callers today rely on being archive-wide; an explicit opt-in
  cannot regress anyone. `changed_ids` keeps its current role and its required status.
- **`pass` semantics do not change.** It stays a function of `SevError` only and is computed on the
  **unfiltered** results, so a severity floor can never turn a failing gate into a passing one.
- **An invalid `severity_min` is an error naming the three accepted values**, not a silent fallback
  to the default: a caller that mistyped a floor asked for something specific and did not get it.
- **The filter lives in `internal/lint`, not in `mcpserver`.** Both tools call the same
  `lint.Filter`, and the severity ordering is a function beside the three constants rather than a
  string comparison at each call site.

**Boundary against [D159](D159-per-concept-lint-opt-out-home-anchored-allow-prefixes.md).** That decision introduced per-concept `lint_ignore`
suppression and explicitly refused a shared default suppression list. This does not reopen it: it
changes what the response *carries*, not what the engine *finds*. A finding suppressed by policy and
a finding omitted from a response for budget reasons are different things and stay different — the
counts are exactly what keeps them distinguishable.

**Invariants kept.** `validation_errors` and `gate_blockers` are never filtered — they are already
error-level and small. The four original top-level keys of the payload keep their names.

**Consequences.** On a KB with the measured distribution the default `gate_check` response drops the
60 advisory findings and keeps the 40 actionable ones, with the full census still in the counts.
Tests in `internal/mcpserver/server_test.go` build a fixture with one finding of each severity and
pin: no `info` in the default response, `counts_by_check` totalling the unfiltered count,
`findings_omitted` consistent with it, `pass` identical at every floor, an invalid floor rejected by
name, and a scoped gate returning only findings under that prefix.
