---
topic: control-plane
---

# D1069 — Lint checks for undated waits, retired supersession links and missing digests

**Decision.** Three lint checks with a definite fix: `wait_undated` (a `waiting_on` with no parseable `review_after` on an open page), `superseded_target_retired` (a `superseded_by` whose target is itself superseded, with the chain ending at a non-superseded concept, fix `set_value superseded_by` to that end) and `digest_missing` (archived entries of a journal that no `digest` of the same journal links, one finding per journal quarter). Coverage of an archived entry is a link from a `digest`-typed concept of its journal.

**Why.** Lint has no severity and no exceptions (D947, D907), so a check stays only if one state of the KB always closes it. A dated wait, the live end of a supersession chain and a digest linking each archived entry are such states. None is auto-repair safe: a date, a successor and a digest content are decisions.

**Alternatives rejected.** Firing on a `deprecated` or `archived` target: that is history kept readable on purpose (D322). Firing on a chain that dead-ends or cycles: no single better state. Deriving a digest's quarter from its id or the archiving date: neither is machine-readable, so coverage is by link. Storing the entry timestamp in the graph facets: needless cache-format change; timestamps are read only for uncovered entries.

**Consequences.** `lint_findings`, `conformance` and `doctor_suggested` may rise on KBs with such debt (no on-disk change). On write, `superseded_target_retired` examines only the written page's own pointer, so retiring a middle page is never refused for a finding on an untouched page; `digest_missing` is not avoidable at write (archiving and the digest are two writes). The chain walk keeps a visited set against cycles.
