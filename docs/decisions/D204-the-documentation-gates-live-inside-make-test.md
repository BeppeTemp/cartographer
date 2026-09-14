---
topic: project-governance
---

# D204 — The documentation gates are Go tests inside `make test`

**Decision.** `internal/repodocs` holds the deterministic checks on the
repository's own documentation — the `AGENTS.md` size budget, the Codex
instruction-chain budget, the freshness of the generated decision index, the
parity of the skill locations, the resolution of every relative documentation
link, and the absence of dead `#dNN` anchors. They are ordinary Go tests, so
`make test` runs them and CI runs them without a single line added to
`.github/workflows/ci.yml`. The index generator is the same test invoked with
`-update`, which is all `make decisions-index` does.

**Why.** Every one of these failures is silent. A chain over
`project_doc_max_bytes` is not an error: Codex stops adding files and the deepest
instructions cease to exist. A skill bridge that became a copy does not warn: two
clients begin following different procedures. A stale index does not complain; it
just stops being true. A moved page does not report its inbound links. A gate
that has to be remembered is not a gate, so it belongs in the command that
already runs on every pull request.

Making the generator and the checker the same code is the point of the `-update`
flag: two separate tools would eventually disagree about the format, and the
disagreement would show up as a diff nobody could explain.

**Alternatives rejected.**

- *A shell script plus a CI step.* It works, but it adds a job that can be
  skipped and a second place where "what CI checks" is defined. The existing
  `test` check is already the required one on `main`.
- *A separate `make lint-docs`.* Same objection: the pattern this repository is
  following is explicit that the budget must live inside something that runs
  anyway.
- *A pre-commit hook.* Not shared with contributors who do not install it, and
  invisible on a fork's CI.
- *`docs/docs_test.go`, next to the pages it checks.* Simpler to find, but mkdocs
  copies unknown files into the published site, so the test source would ship
  with the documentation. `internal/repodocs` finds the repository root by
  walking up to `go.mod`, so it does not care where it runs from.
- *Checking anchors as well as file existence in links.* Deliberately not done:
  it needs a markdown parser to be correct about generated heading ids, and the
  cheap version produces false positives on the very pages that document the
  KB's own link syntax.

**Consequences.** Adding a decision without regenerating the index fails CI, and
the error says which command fixes it. Growing `AGENTS.md` past 120 lines fails
CI, and the message says to move a section to the page that owns it rather than
to raise the limit — raising it is possible, but it is then a visible edit to a
constant with a comment explaining which client enforces it. The link check
strips fenced blocks and inline code first, because the documentation contains
markdown link *examples* describing the KB's own format and they are
illustrations, not links — the same distinction D150 draws for the KB linter.
Scope is the documentation surface (root pages, `docs/`, and the skills), not
`test/` fixtures or the KB skills bundled into the binary, whose links point
outside this repository.
