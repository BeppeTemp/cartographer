---
topic: architecture
---

# D1937 — a server plugin supplies role templates and presets, as data the operator maps and the writer applies

**Decision.** The server side of a plugin has two data families. `roles/<name>.yaml` is a role template: allow rules
with no `kb`, taken by an operator's `auth.roles` entry with `from: <plugin>/<role>` and `kbs: [...]`, expanded at start
into one copy of each rule per KB and judged by the same `ValidateAuthRoles` as a role written by hand. `presets/<name>/`
is a map contract (the `map_create` arguments) plus the templates it names, listed by the advanced read-only tool
`preset_list` and applied by the writer through `artifact_write` and `map_create` under its own scopes. Both are
validated at load with the validators of the tools that will use them, and a bad file stops the start naming the plugin
and file. The loaders live in `internal/pluginserver`, not in `internal/plugin`.

**Why.** Roles are the server's own authorization, so a plugin can carry them without a KB behaving differently per
server; a template or a contract served live from server config would make a KB behave differently on every server that
mounts it (D746, D906), so presets only hand data over and the KB is changed through ordinary, audited writes. The
operator keeps the authority: the plugin never names a KB or a token, the role the token references is the operator's
own name (a duplicate is refused, so a plugin role cannot shadow one), and the grammar has no deny rule, so a plugin can
only offer rules that the operator then grants explicitly. `internal/plugin` is inside the client import firewall and
cannot reach `kb`, `lint` or `mcpserver`, where the template and contract validators live; a registering package on
the server side keeps one judgement for what is written and what is loaded.

**Alternatives rejected.** Validating templates and contracts with a copy of the rules in `internal/plugin`: two
judgements drift. Moving the template validation into `internal/kb`: it depends on `lint` heading folding and would
move 200 lines for a four-line wrapper. Roles that apply to every KB (`kbs` optional): a plugin would widen a token
beyond what the operator named. Namespacing a plugin's roles into `auth.roles` automatically: a role would exist the
operator never wrote. Presets written into a KB by the server: the server would write KB content no principal asked for.

**Consequences.** A plugin role needs an operator entry per use; renaming a KB must also update `kbs` (`kb rename`
reports it). The expansion runs on the plugins loaded at start, also on every auth reload, so a pin change still needs a
restart. A new server family goes in `internal/pluginserver` and registers with `plugin.Register`; its loader must
refuse an executable file and any file outside its shape.
