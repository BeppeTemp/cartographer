---
topic: deployment-release
---

# D711 — `kb create` / `kb clone` can record their `kbs[]` entry

**Decision.** With `--artifact-write`, `cartographer kb create` and `kb clone`
upsert a `kbs[]` entry (`path`, `allow_artifact_write: true`) in the server
config, so the KB mounts configured instead of discovered. Without the flag
neither command opens the server config, as before.

**Why.** Turning artifact writes on for a freshly created KB meant hand-editing
the server YAML and restarting. The flag removes the edit, not the choice:
artifacts (skills, agents, MCP descriptors and hooks) reach client machines
through `sync` (D71), so enabling writes stays an explicit operator decision.
The config is rewritten through a `yaml.Node` document (the `kb rename`
machinery, D177) so comments, order and unknown fields survive, and re-loaded
after the write (restored on failure). The preflight runs before anything is
created, so a missing, invalid or ambiguous config leaves nothing behind. A
config-write failure after a successful create keeps the KB (D156 stance) and
prints the entry to add by hand; a failed push writes no entry, since the
next start would fail on a path the guidance tells the operator to delete.

**Alternatives rejected.** On by default: silently widens what reaches client
machines (D71). A `kb config <kb> key=value` command: a general config editor
is out of scope; an existing discovered KB is still fixed by hand.

**Consequences.** Amends D85/D151/D177's "kb create leaves no `kbs[]` entry"
for the flagged case only. `kb rename` finds the entry by path, so it keeps
working. The entry records the absolute path used, independent of `data:`.
