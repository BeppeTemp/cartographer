---
topic: sync-provisioning
---

# D359 — The OpenCode hook plugin is generated in the shape of the installed major

**Decision.** The generated `cartographer-<name>.js` plugin (D59) has two shapes, chosen
by the installed OpenCode's major (`opencode --version`): 1.x keeps the named export
returning a hooks object; 2.x is `export default { id, setup(ctx) }`, with
`ctx.tool.hook("execute.before" | "execute.after")` for tool hooks and
`ctx.event.subscribe` filtered on `event.type` for session events. A version that cannot be
read (client not on PATH) keeps the 1.x shape. Each file carries
`cartographer:opencode-plugin-shape=<major>` in its header; the provider registry declares
`HookPluginMajors: [1, 2]`, and `doctor` reports a file whose shape differs from the
installed major (error) or an installed major outside that list (warning).

**Why.** OpenCode 2.x rejects the 1.x shape at load ("Plugin must export a default
definition with an id and an effect or setup function", #623), so no KB hook ran there,
while the file on disk looked right. Source: the OpenCode 2.x plugin docs
(`https://opencode.ai/v2/docs/build/plugins/`) and the V1 migration page
(`.../plugins/migrate-v1`: "V1 plugin implementations do not run in V2"; `event` becomes
`ctx.event.subscribe()`; `tool.execute.before` becomes `ctx.tool.hook("execute.before")`).
The 2.x script runs through `node:child_process` (`sh -c`) and throws on a non-zero exit, so
a blocking hook still blocks as `$` did on 1.x; the 2.x context documents no `$`.
The plugin is a plain object, not `Plugin.define(...)` from `@opencode/plugin`: a file in the
plugins directory should not depend on a package import resolving there.

**Alternatives rejected.** One file satisfying both loaders (default export carrying `id`,
`setup` and a 1.x `server`): 1.x accepts an object entrypoint only from 1.18.29, and nothing
verified how older 1.x treats a default export. Writing 2.x unconditionally: breaks every
1.x user. Reading `opencode.log` for "failed to load plugin": the log path and format are not
a documented interface; the version comparison catches the same case offline.

**Consequences.** Nothing re-runs the generator when OpenCode is upgraded: `doctor` flags the
mismatch and `cartographer sync` rewrites the file (registration rewrites it whenever the hook
is applied). Not verified against a live 2.x: that a plain object satisfies the loader the same
as `Plugin.define`, the global `~/.config/opencode/plugins/` autoload in 2.x (the docs name
`.opencode/plugins/`), and that `ctx.event.subscribe` delivers `session.created` (the docs
show no session-start example). A new OpenCode major needs a new shape and an entry in
`HookPluginMajors`.
