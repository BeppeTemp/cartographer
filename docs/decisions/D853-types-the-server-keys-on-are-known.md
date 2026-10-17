---
topic: data-plane
---

# D853 — Types the server keys on are known to `unknown_type`

**Decision.** `internal/kb/keyedtype.go` is the one list of the concept types the server keys on: `Source` and `Contradiction` (matched exactly), `Service` and `digest` (matched in any case, D158, D322). `unknown_type` skips such a type when no declared type matches it case-insensitively and the page's map is not strict. In a strict map that does not list it the warning stays, with the fix "add it to the map's concept_types (map_update), or move the page".

**Why.** `source_register` writes a `Source` page, and the tools find the ledger by that type (D278). In a KB with a type palette the page got `unknown_type`, and neither suggested fix worked: changing the type takes the page out of the ledger, and `template_proposal` never fires below three pages. The palette stays what the KB declares, so `lint.Inactive` (D357) and the message are unchanged. The cost is a second source of truth for "known", the exemption, kept next to the predicates the match sites already use.

**Alternatives rejected.**
- Keyed types in the palette: it would activate `unknown_type` on KBs with no vocabulary (D357), list undeclared types in the message, turn the case fix of `source` into a ledger entry and of `contradiction` into a gate blocker, and leave lint silent in strict maps where `validate` errors.
- A built-in `Source` template: changes the template catalogue and `concept_new`, and covers one type of four.
- `source_register` or `map_create` writing a template or `concept_types`: per-KB state as a side effect, against D278's explicit map creation.
- Documenting `lint_accept`: a finding whose only resolution is dismissal is noise.

**Consequences.** A new type the server keys on is added to `keyedtype.go` and matched through its predicate, never with a literal. A misspelled keyed type (`source`) stays an info finding with no fix and sits silently outside its ledger; a dedicated finding is a follow-up, as is `template_proposal` skipping keyed types (#832).
