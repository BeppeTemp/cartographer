---
topic: architecture
---

# D1970 — a pinned plugin ships exec recipes as files of its tree

**Decision.** The `surveyor` side of a plugin has a `recipes` family (`recipes/<name>/` with `instructions.md` and
`recipe.yaml`), loaded by `internal/surveyor`. A pinned plugin's recipes are appended to the Surveyor's `ExecRecipes`
under their `<plugin>/<name>` name, with the command resolved to the read-only store; the registry, the poll and the
unit executor gain no code path.

**Why.** Code a KB can select is code anyone with write access to a KB can run, so the only consent is the digest the
operator pinned (D1936). Shipping the command as a file of the digested tree makes the approval cover the code, and
the preview can list every executable under "runs code".

**Alternatives rejected.**
- A `command` looked up on PATH: the digest would not cover what runs.
- A plugin naming operator variables in its own files: the plugin author would choose which of the operator's secrets
  it reads. The manifest declares names, the operator's config maps them, and a mismatch either way stops the start.
- Judging the env mapping in the loader: a point loader sees one folder and no manifest, so it runs when the pinned
  plugin is open, naming `plugins.<name>` and its line.

**Consequences.** The loader lives in `internal/surveyor`, not `internal/plugin`, which is the client half; the family
is registered in `internal/plugin/core` so every side reads a tree the same way. A recipe that does not answer
`spec`, `check` or `list` is a failure of `source test`, never of the load, which runs no script.
