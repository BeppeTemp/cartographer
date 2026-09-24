---
topic: sync-provisioning
---

# D263 — A KB declares its path placeholder registry

**Decision.** A KB declares the `{{path:…}}`/`{{repo:…}}` keys it may cite in one
KB-root file, `paths.yaml` — `paths:` and `repos:` sections, each key a slug with a
required `description`, an optional home-anchored `default` and, for a repo, an
optional `remote`. It is authored like the other KB-root artifacts (git, or
`artifact_*` with the `allow_artifact_write` gate, validated strictly on write) and
is never materialized: `sync_pull` serves it parsed as `path_registry`. A client
resolves a key from `.cartographer.yaml` `paths:` first, then — for a repo — from
the repo index, by the declared remote when there is one, then from the declared
default if it exists on this machine (a live clone, for a repo), and otherwise
leaves it unresolved. When the KB has the file, lint reports a concept citing an
undeclared key (`unknown_placeholder`) and a declared key nothing cites
(`unused_placeholder`).

**Why.** After D262 the client sees every key and asks for the missing ones, but
nothing on the authoring side stopped keys being coined per page — one real KB had
three overlapping keys for one tool's configuration and some twenty keys over forty
concepts — and most `path:` keys are the same on every machine (`~/.claude`,
`~/.ssh/config`), yet each client had to map each of them by hand. A declared
vocabulary with portable defaults removes both: agents reuse keys, and a fresh
client resolves the common ones with no configuration. The cost is one more file
format to validate, one more unsigned field in `sync_pull`, and one more lock field.

**Alternatives rejected.**
- *Declare keys in each Map's `_map.md` contract (D107/D124).* The same key is cited
  from several Maps; a per-Map declaration duplicates it and lets the copies drift.
- *A Markdown concept instead of YAML.* The registry is data read by tools (sync,
  lint, the connect step), not prose for readers; a concept would need a nested
  frontmatter grammar the OKF subset does not have.
- *Materialize the file on clients.* Nothing on the client reads it as a file; it is
  input to resolution, so it travels as data next to `placeholders`.
- *Sign the registry, or put it in the revision.* It can only propose a path under
  the reader's home, only as a fallback after the operator's own mapping and the repo
  index, and only when that path exists; it executes nothing. Its trust is that of a
  concept body, and like `placeholders` (D262) a change to it is not a catalogue
  change. The client re-checks the home anchor itself, so no server can make it
  propose an absolute path.
- *Allow absolute or relative defaults.* A registry is read on every machine: a
  default of `/Users/x/…` is one author's machine hardcoded into everybody's, the
  exact problem D75 removes.
- *Let a default win over the repo index, or use one that does not exist.* A default
  never overrides an explicit mapping or a found clone, and a default that is not
  there is an unresolved key — reported, askable — not a wrong path handed to an
  agent.
- *Fail on two KBs declaring one key differently.* Resolution must never block a
  sync (D75 WP3): the first KB in the provider's KB order (D182) wins and one warning
  per sync names both.
- *Lint every KB for undeclared keys.* Existing KBs would be flooded on upgrade; the
  checks are opt-in by the file's presence, and an unparseable file is one
  `contract_malformed` finding rather than every key flagged.

**Consequences.** A KB without `paths.yaml` behaves exactly as before. A declared key
nothing cites is still offered to resolution: if it resolves it joins the "Local
paths" table (an agent about to write learns it exists), otherwise it is dropped
silently — it is never reported unresolved. The lock records each key's declaration
(`placeholder_decls`) so `cartographer paths list` and the connect step show the
description and pre-fill the default without a network call; an older lock has none,
meaning "nothing declared". The instructions-block paragraph gains the authoring
rule (cite only declared keys, declare a new one in the same change), which changes
the paths-section hash and so rewrites every block once. `paths.yaml` is not in
`artifact_list` or the Atlas UI's artifact routes: those list what reaches a client
as an artifact.
