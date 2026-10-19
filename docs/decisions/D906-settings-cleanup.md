---
topic: client-configurator
---

# D906 — Settings cleanup: one home per key, and no setting that turns a safeguard or a feature off

**Decision.** The settings shrink to the keys teams legitimately need to set differently, each in one file: server
infrastructure in the server YAML, a KB's behaviour in its `settings.yaml`, a machine's choices in
`.cartographer.yaml`. Four principles decide what stays: the lint cannot be escaped (P1), one home per key (P2),
nothing tied to one git provider (P3), and a new KB works with no keys at all (P4). Every removed key goes into the
realignment registry of D903: it is ignored with a warning that names what replaces it, and dropped when the file
is realigned. What each part removes, and what replaces it:

- **Artifact writes are always allowed.** `allow_artifact_write` and `allow_template_write` are removed, from a
  KB's `settings.yaml` and from `kbs[]`. `artifact_write`, `artifact_delete` and `artifact_move` are registered on
  every KB, and a token's `rw` scope on the KB is the only gate, as for every other write. The repairs that
  rewrite artifact files apply like the others, from `kb_repair` and from the background repair. The
  `--artifact-write` flag of `kb create` and `kb clone`, which existed only to write the setting (D711), is
  removed, and `kb_status` no longer reports `artifact_write` and `template_write` capabilities.

**Why.** The settings grew with the features, often on two or three levels, and many of them switched something
off. A long-lived installation ends up configured almost at random, and the Atlas cannot show a configuration
clearly when most of it is switches nobody chose (#890).

The artifact-write right guarded the fact that a written skill reaches the clients that sync the KB. It did not
guard it: anyone who can push to the KB's remote writes the same files through git, and an MCP token with `rw`
scope is granted per KB by the operator already (`auth.tokens`). What the switch did add was a KB that worked for
concepts and silently refused artifacts, which is what a discovered KB, with no `kbs[]` entry to carry it, always
was. The template right (D352) existed only to soften that default.

**Alternatives rejected.**
- Keeping `allow_artifact_write` with a default of true: a switch that nobody needs to turn off is a key the
  configuration does not need (P4), and the off position would keep the silent refusal.
- Keeping `--artifact-write` as an ignored flag: the command is run by hand or by the `kb-create` skill, which
  ships with the binary, so no stored invocation outlives the release.

**Consequences.** Breaking: the removed keys and the `--artifact-write` flag. A token with `rw` scope on a KB can
now write what that KB provisions to its clients (skills, agents, hooks, MCP descriptors, instructions): an
operator who wants a token that only reads gives it `kb:<name>:r`.

A future change here must preserve all of these:
- a removed key is never an error that stops a start or a mount: it is ignored, named in a warning with what to
  do instead, and dropped by the realignment;
- the token's scope stays the gate of every write, artifacts included.
