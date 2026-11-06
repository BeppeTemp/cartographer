---
topic: control-plane
---

# D1731 — the client file is grouped by what a key concerns: server, placeholders, providers, knowledge bases

**Decision.** `$HOME/.cartographer.yaml` stops being a flat list of root keys and is grouped by subject. The layout contract, shared by every change that moves keys:

- `server:` holds `url`, `name`, `auth`, `token` and `allow_insecure_http`: what the machine needs to reach the server.
- `placeholders:` holds `search_roots`, `paths` and `ignored`: how `{{repo:}}` and `{{path:}}` resolve.
- `defaults:` (`unbound_access`), `providers:` (per provider: `kbs`, `scope`, `unbound_access`, `config_dirs`, `access`, `tools`, `workspaces`) and `knowledge_bases:` (per KB: `access`, `artifacts`, `identity`, `signing_keys`, `default` for every other KB) take the per-provider and per-KB maps in a later change; the root keeps `update_check` and `usage_scan`.
- It is `knowledge_bases`, not `kbs`: `kbs:` is still read as the legacy alias of the `known_kbs` cache for pre-D169 files, and reusing it as a map would make every old file ambiguous.

The first change moves the `server:` and `placeholders:` keys (`server_url` to `server.url`, `server_name` to `server.name`, `auth`, `token` and `allow_insecure_http` into `server`, `search_roots` and `paths` into `placeholders`, `ignored_paths` to `placeholders.ignored`) with the `Moved` primitive (D1725): one entry each in `Retired`, no hand-written rewrite.

**Why.** A reader had to compose a provider, a KB or one feature from keys scattered over the file, and the code that wrote them had to recompose them too. A move keeps the value and the comment a person wrote, so the migration is a mechanical rewrite and not a loss.

**Migration.** The first `sync` after the upgrade realigns the file and keeps the original as `.cartographer.yaml.bak-<version>`. `Save` over a file the realignment has not reached moves the keys in the same rewrite, so a save never leaves a flat key beside its group. `Load` reads both layouts for at least one minor, the grouped key winning, so a file on a read-only mount keeps working. A key set at both places with different values is refused by `Load`, `Save` and the realignment, which leaves the file untouched: the operator keeps one. Everything that masks the bearer token (the report a client posts, the server's second mask, `cartographer config`) knows the token at either place, and the server maps a flat key reported by an older client to its new path.

**Alternatives rejected.** The lighter variant (only `server:` and `placeholders:`) removes half the disorder and leaves the provider and KB spread, which cost a reader most. Writing the new layout only on an explicit command: a file would stay flat until someone remembered, and every release would read both forever. A hand-written migration in `prepareRealign`: the primitive exists for exactly this.

**Consequences.** Schema paths of the moved keys are the grouped ones (`server.url`, `placeholders.ignored`); the settings report, `cartographer config` and the Atlas view list them so. The order of the keys in an already-written file is kept (a moved group lands at the end); writing the schema order is a later change. Dropping the flat names from `Retired.Moved` and from `Load` is a separate change after at least one minor.
