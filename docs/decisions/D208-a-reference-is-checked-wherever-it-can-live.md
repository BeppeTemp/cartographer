---
topic: project-governance
---

# D208 — A reference is checked wherever it can live, and a decision number has three states

**Decision.** The reference gates read **every** tracked text file — `.go`, `.md`,
`.sh`, `.yaml`, `.yml` — minus `CHANGELOG.md` and the fixture KBs, collected by a
filesystem walk. Two things are then required of the corpus:

- a cited `D<n>` resolves to a record, **or** is declared in `GapDecisions` (a
  number that deliberately has none and must never be reused), **or** in
  `ReservedDecisions` (a number an open plan issue holds until it lands);
- a cited `docs/…` or `.agents/skills/…` path exists.

`KnownDanglingDecisions` is gone, replaced by those two typed maps.

**Why.** D204's version of the gate read only `.go` files under `cmd/` and
`internal/`, plus the `AGENTS.md` files. D206's own account of what it caught says
the bad reference lived in `internal/provisioning`, in `docs/sync.md` and inside
another decision record — so two thirds of the evidence came from reading, not from
the gate, and the gate was given credit for the whole find.

Widened to 514 files, it immediately produced one more: `D130`, cited by D128,
D129 and D133 as the decision that specifies what happens after the handshake era
is retired, with no record anywhere. Same defect as D163, in the same archive,
three weeks later, in the files the gate did not read.

The path variant produced more. Splitting the ten thematic registers into one file
per decision left **19** pointers to the deleted registers in nine packages and
three e2e scripts — `see docs/decisions/<register>.md D60` and the like — and
turned up four older ones nobody knew about: two pages under a `docs/plans/`
directory removed long before, and the roadmap page that the very decision which
deleted it still cited by path. A page rename is reported by the compiler for
nothing, by mkdocs only for markdown links, and by the markdown link check only
inside `.md` files. A pointer in a comment is checked by no one, and it is the
pointer an agent follows.

**The three states.** `CONTRIBUTING.md` says a plan issue's title reserves its
`D<n>` before any file exists, and `make decisions-next` is documented as only half
the answer for that reason. So a forward reference to a not-yet-written record is
*correct* — and there was no way to say so: the only vocabulary was
`KnownDanglingDecisions`, an untyped allow-list that D206 rightly wanted empty.
Typing the states separates "never existed, never reuse it" from "not yet", and
each is checked in both directions: a declared gap that acquires a file fails, and
a reserved number whose record has arrived fails until the entry is removed.

**A filesystem walk, not `git ls-files`.** A decision file that has been written
but not yet staged has to be in scope. Reading the index instead would make the
gate pass locally and fail in CI, which is worse than not having it.

**Alternatives rejected.**

- *An inline marker per citation* — `D163 (gap)` — which is what the first version
  did. It forces editing a record's prose, and even its title, to satisfy a
  scanner: D206's own heading contains a bare `D163`. It also says nothing about
  reuse, which is the half that actually protects the archive.
- *Keep `KnownDanglingDecisions`.* One list for two different facts. The entry for
  a reserved number and the entry for a retired one look identical, so the list
  cannot be pruned safely — and D206's argument against a stale allow-list applies
  to it first.
- *Resolve `D130` by writing the record its plan planned.* The plan was closed as
  overtaken; the record is that it was not done (D130).
- *Restrict the path check to markdown.* That is the existing link check, and it is
  the check that saw none of the 23.
- *Check heading anchors as well as file existence.* Unchanged from D204: it needs
  a markdown parser to be correct about generated ids, and the cheap version
  produces false positives on the pages documenting the KB's own link syntax.
- *Scan `.json`/`.toml` too.* A quoted `"D12"` in JSON matches the bare-reference
  regex by construction, and no reference lives there today. The two YAML files
  that do carry them (`.goreleaser.yaml`, `config.example.yaml`) are in scope.

**Consequences.** `CHANGELOG.md` is excluded and stays excluded: it is a released
historical record that legitimately names paths as they were, and editing it to
satisfy a gate would be falsifying it. Fixture KBs are excluded because their
references belong to the fixture. Renaming a page under `docs/` now means fixing
its pointers in code in the same change — a build failure instead of a discovery
months later — and the cheapest way to avoid the problem entirely is the
convention this repository already has: cite the bare `D<n>`, which survives both
a rename and a re-titling. `TestReferenceCorpusIsSane` asserts that the corpus
contains the specific files these gates exist to read, because the failure mode of
a corpus definition is to quietly match nothing and pass.
