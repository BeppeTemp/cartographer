---
topic: skills-services-secrets
---

# D1373 — Source authoring: a read-only dry test and a bundled skill

**Decision.** `cartographer worker source test` runs the listing the worker will run, with the real recipe, through
the poll's own registry, preconditions and listing function, and writes nothing: no lease, cursor, Source, unit or
log entry, no model call. Its exit codes are `0` listed, `1` the poll would record a `feed_error` (printed with the
poll's code and text), `2` the test could not start, `3` a system recipe's preflight refused (nothing listed). A system recipe goes through the poll's preflight, state `Load`, listing validation and diff; the state is never written. A
bundled skill, `cartographer-kb-source-author`, drives an operator from a source to a tested lister, a registered
Source (one `source_register` call carrying `recipe`) and a deploy change. The lister lives in the operator's
repository and is referenced by the recipe's `command`; the skill demands a read-only identity for every shape and
ships references, no script, and is not served by `kb_guide`.

**Why.** A lister and its worker config were found by trying them against the deployed worker, where a mistake is a
`feed_error` read the next morning. A dry test that shared code with the poll cannot pass a script the worker then
refuses; one that carried a copy could. "Writes nothing" is structural: in `--source` mode the KB is reached through a
caller that offers only `concept_read`, `concept_list`, `asset_read` and `asset_list`, so a write is not expressible.

**Alternatives rejected.** A `--json` output (the plain tab-separated text is the contract the skill reads; a second
format doubles it). A copy of the poll's checks inside the command (it drifts). Requiring every `*_env` variable at
load (an author's machine has neither the model keys nor, in `--recipe` mode, the token): the loader keeps full
structural validation and narrows only the environment reads (`EnvScope`). Storing the lister in the KB: whoever
writes a KB would run code on its readers' machines (design D6, D19). Serving the skill through `kb_guide`: it needs a
shell, an editor and a conversation (D1100).

**Consequences.** The poll's preconditions (`feedPreconditions`), listing step (`listFeed`) and system steps (`observeSystem`) are the only
implementation; a change to either changes the dry test. The guarantee covers the worker and the KB, not what the
operator's own script does, which is why the skill requires a read-only identity. The dry test never contacts the
server in `--recipe` mode. A hand-kept list of bundled skills in several tests is extended together with a new skill.
