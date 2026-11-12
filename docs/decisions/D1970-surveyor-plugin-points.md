---
topic: architecture
---

# D1970 — a pinned plugin ships exec recipes and connectors as files of its tree

**Decision.** The `surveyor` side of a plugin has two families, `recipes` (`recipes/<name>/` with `instructions.md` and
`recipe.yaml`) and `connectors` (`connectors/<name>.yaml`), loaded by `internal/surveyor`. A pinned plugin's entries
are appended to the Surveyor's `ExecRecipes` and `Connectors` under their `<plugin>/<name>` name, with the command
resolved to the read-only store; the registry, the poll and the unit executor gain no code path.

**Why.** Code a KB can select is code anyone with write access to a KB can run, so the only consent is the digest the
operator pinned (D1936). Shipping the command as a file of the digested tree makes the approval cover the code, and
the preview can list every executable under "runs code".

**Alternatives rejected.**
- A `command` looked up on PATH for recipes: the digest would not cover what runs. Connectors keep a bare name because
  a stock MCP server is usually installed, not shipped, but the preview flags it.
- A plugin naming operator variables (`env: {TOKEN: OPERATOR_VAR}` in its own files): the plugin author would choose
  which of the operator's secrets it reads. The manifest declares names, the operator's config maps them, and a
  mismatch either way stops the start.
- Judging the cross-checks (env against `permissions.env`, a URL host against `net`, a recipe's connector against the
  plugin's connectors) in the loaders: a point loader sees one folder and no manifest, so they run when the pinned
  plugin is open, naming `plugins.<name>` and its line.
- Plain http connectors to a loopback name: left to the private-network grant, a later change.

**Consequences.** The loaders live in `internal/surveyor`, not `internal/plugin`, which is the client half; the
families are registered in `internal/plugin/core` so every side reads a tree the same way. A recipe that does not answer `spec`, `check` or `list` is a failure of
`source test`, never of the load, which runs no script.
