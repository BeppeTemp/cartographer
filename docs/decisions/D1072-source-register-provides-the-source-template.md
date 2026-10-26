---
topic: control-plane
---

# D1072 — source_register provides the source template

**Decision.** The first `source_register` into a ledger journal writes
`templates/source.md` when it is absent (built by `kb.SourceTemplate` from the
server's own shape of a Source) and, only when the journal holds `Source`
concepts only and every one of them already follows the template, sets
`templates: [source]` and `default_template: source` on the map. Nothing that
exists is overwritten or overridden; the response carries `notes`, and a setup
failure is a `warnings` entry, never a refusal.

**Why.** The server knows the shape of a Source, yet the ledger started without a
template and `kb_review` proposed one only after a few pages, from a vocabulary
limited to what it had seen. The template is a visible KB artifact (D109) the
operator can read and widen, not a bundled fallback resolved behind the KB's
back. `source_register` is exempt from the write gate, so it must not introduce a
finding by itself: declaring templates makes a map strict (D371, D907), hence the
Source-only and already-conforming preconditions. It costs a write into the KB
beyond the concept, in one commit.

**Alternatives rejected.** A bundled fallback template: invisible and not
editable. Declaring the map unconditionally: a mixed journal or an older page
would start failing `template_missing` / `template_field_missing`. Declaring
`default_template` alone: `UpdateMapContract` refuses it unless the slug is also
in `templates`, so both keys are set (the plan assumed `templates` stayed unset).

**Consequences.** A Source journal becomes strict once declared; the pages the
tool writes conform by construction (`timestamp` is optional because it is
omitted when the caller gives none, sections are open). The vocabulary lives in
one place, `kb.SourceIngestStatuses`, pinned by a test.
