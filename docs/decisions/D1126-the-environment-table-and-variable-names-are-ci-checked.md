---
topic: project-governance
---

# D1126 — The environment table and variable names are CI-checked

**Decision.** Two tests in `internal/config/envdocs_test.go` fail the build when the
`docs/deployment.md` §Environment variables table and `config.Schema` disagree (variables and
flag cells, both ways, with a reasoned list of rows that are not Schema keys), and when a
`CARTOGRAPHER_*` name in a string literal of non-test Go is neither in a current-state page,
nor removed in `config.Retired`, nor allow-listed with a reason.

**Why.** The table is the page a server operator reads first and nothing compared it with the
Schema that carries the same facts; a new variable could ship undocumented without any signal.

**Alternatives rejected.**
- Generating the table: its Description column is long prose (precedence, retired notes,
  defaults) that the Schema's one-sentence description does not replace; the keys are tested,
  the prose stays hand-written.
- Putting the test in `internal/repodocs`: the failure belongs beside `schema.go`, which is what
  the message tells the contributor to edit, and `repodocs` must not import `config`.
- Scanning comments or `_test.go` files: an example or removed name in a comment is not a
  contract, and a test-only variable needs no allow-list entry.

**Consequences.** A Schema key that gains an `Env` fails until its table row exists. Allow-lists
(`nonSchemaEnv`, `undocumentedEnvAllow`) follow the `GapDecisions` pattern (D206): a reason is
mandatory and an entry that became unnecessary fails. Shell-read variables (installer,
workflows) are out of scope.
