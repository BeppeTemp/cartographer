---
topic: data-plane
---

# D1263 — A synthesis hypothesis is a gap Contradiction, written behind a worker-side guard

**Decision.** What the worker's synthesis task infers (a relation nobody linked, a
subject with no page, statements that may disagree) is recorded as a `Contradiction`
whose `contradiction_kind` is `missing_context` or `open_question`, never as another
type or kind. The evidence is its `provenance`; three optional keys carry the rest:
`origin: synthesis`, `confidence: low | medium | high` and, written by whoever closes it,
`resolution_outcome: confirmed | rejected`. Its id is deterministic
(`<map>/synthesis-<kind>-<8 hex of kind + sorted involves>`), and every model call goes
through a guard in the worker (`internal/worker/synthesis`) that creates only that id when
absent and patches only an open synthesis item's evidence, confidence, `provenance` and
`involves`.

**Why.** A gap kind is already non-blocking, listed by `work_list` (reason `gap`),
counted in `open_gaps` and closed by the existing lifecycle; a machine guess must never
block a person's write. The server cannot tell "a claim on an `active` page" from a new
page (`concept_write` overwrites an id without `if_match`), so D11 is enforced client-side
on top of D9. Stated residue: a token that can write concepts could, by a worker bug,
overwrite a page; the guard and its tests are the defence.

**Alternatives rejected.** A `Hypothesis` type (same lifecycle, a public-contract change
for no capability); a third reserved gap kind (a server older than the worker would treat
it as blocking, D273); a per-type deny rule on the server (out of scope); a worker-side
state file for rejected hypotheses (D13: a rejected one stays a resolved concept with
`resolution_outcome: rejected`, and its id exists, so it is never proposed again).

**Consequences.** `origin`, `confidence` and `resolution_outcome` are free-form to the
server and may trip `missing_value_contract` once enough exist; that is documented, not
suppressed. A synthesis write never targets an existing concept and never uses a blocking
kind: the constant check and its test live in the package.

**The task kind.** `synthesis` is a registered kind (`internal/worker/synthesis.go`) with
five `tasks[]` options (`synthesis_*`, in the key table). It sits last in `tasks`, so the
runner's priority order is the plan's priority order (D12); one run is `Candidates`, then
one model call per candidate behind the guard, with a ceiling of three times the estimated
cost. The guard checks that every `provenance` entry exists and that every Evidence line
links one, and sets the commit reason `synthesis: <id>` over the model's. The run record is
the summary line of the closing entry, ending with the keys examined. The instructions are
embedded in the binary (D15), and the package defines its own `ToolCaller` so the kind in
`internal/worker` can import it.
