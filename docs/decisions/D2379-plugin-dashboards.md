---
topic: architecture
---

# D2379 — A plugin preset carries dashboards as data, and the agent writes them into the KB with `dashboard_patch`

**Decision.** A preset of a server plugin may carry `dashboards/<name>.yaml`, judged at load by `kb.ValidateDashboard` (shape only: a field or concept id a widget names is not checked, because a preset is written before the KB it lands in). `preset_list` returns each dashboard as `{name, path, title, description, ops}`, `ops` being the exact array of one `dashboard_patch {name, if_match: "", ops}` call, computed by the pure `kb.DashboardFileOps`. The agent applies them on request and the KB owns the result as an ordinary versioned file; nothing is served live from the plugin. A preset with no map contract is allowed when it has at least one dashboard and no template (`mcpserver.CheckPreset`), `kind` and `create` are then absent from its `preset_list` entry, and the `presets` family is at level 2. The server-side preview lists the dashboards a preset carries.

**Why.** A KB's behaviour is carried by the KB, not per server (D746, D906), so a dashboard is data the agent writes, never code or a view the server computes. The plugin tree is pinned by digest, so any byte of a dashboard is part of the operator's consent, and the same validator that guards the tool guards the loader: a tree that passes the preview loads at start. Returning ops, not the file, spares the agent a YAML-to-ops conversion it would get wrong and does not double the 256 KiB budget for the same information. The cost is that a dashboard may name a field a given KB lacks; the widget then reports its own problem when drawn.

**Alternatives rejected.**
- Serve the plugin's dashboards live from the server: a second source of truth for a KB's dashboards, outside its history and its scopes.
- Return the raw file and let the agent translate it: error-prone, and the text counts against the same cap.
- A new tool to apply a preset's dashboard: `dashboard_patch` already creates, under the caller's own scopes, with no new write path.
- Overwrite an existing dashboard on apply: `if_match: ""` on an existing name is refused, so an edited dashboard is never lost.
- Keep `kind` mandatory with a placeholder for a dashboard-only preset: `kind` is a `map_create` argument and has no value without a map.

**Consequences.** `preset_list` stays read-only, advanced and identical for every principal: ops are server data like template contents. `kb.DashboardFileOps` and `ValidateDashboard` must stay pure (no `*KB`). A plugin that carries `dashboards/` declares `sdk: {presets: 2}`; an older server marks it `unsupported`. A preset past the result cap is listed with dashboards without `ops` and `truncated: true`.
