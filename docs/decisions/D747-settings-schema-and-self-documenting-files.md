---
topic: client-configurator
---

# D747 — One settings schema in code documents both YAML files from inside

**Decision.** Each settings file has one key table declared in code (`internal/settingschema` holds the type and the
renderer; `internal/config/schema.go` and `internal/clientconfig/schema.go` hold the tables): path, scope (`server`,
`kb`, `client`), type, values, default, description, the env var and flag that override it, `restart`, `secret`. The table
feeds the comments written into the file, the generated `config.example.yaml`, and the later config view and KB/server
split. The client `Save` is a `yaml.Node` round-trip, and the three server-owned caches (`known_kbs`,
`server_mount_mode`, `server_routed_path`) move to `.cartographer-state.json`.

**Why.** Both files were undocumented in place, and the only commented copy, `config.example.yaml`, was hand-written and
drifted. The client `Save` marshalled a map, so every comment was dropped on every save, and because the caches were
rewritten by every sync the file was rewritten constantly. Splitting the caches off makes the user file change only when a
person changes a setting, which is what makes keeping their comments tractable.

**Alternatives rejected.**
- Struct tags as the source: the server has a raw/public struct pair plus env and flag layers, so tags would be
  duplicated and still miss the overrides. A reflection test keeps the table and the structs in sync instead.
- One merged file for client and server: owners and lifecycles differ (a remote server's config is GitOps, the client's is
  on the workstation) and the local service pins auth off (D268).
- Keeping the caches in the YAML and fighting the rewrite churn: every sync would still touch the file a person edits.
- Hand-maintaining `config.example.yaml`: it is the thing that drifted.

**Consequences.** A new YAML key needs a schema entry or the build fails. A file with no comments loads and behaves as
before, comments being additive. A comment the user wrote, and any key the program does not know, survive every save; the
schema only documents keys that carry no comment. An existing installation upgrades with no action: `Load` reads the
caches from the YAML while there is no state file, the next save moves them and drops them from the YAML, and a corrupt
state file is treated as absent. Release note: client files are rewritten with comments at the next save and lose the
three cache keys. Scope `kb` marks the `kbs[]` keys that a KB may later carry itself.
