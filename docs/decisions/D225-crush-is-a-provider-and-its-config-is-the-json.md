---
topic: client-configurator
---

# D225 — Crush is a provider, and the file we write is the deprecated JSON

**Decision.** Crush joins the registry as `crush`, with MCP, instructions and
skills, and with `agent` and `hook` declared unsupported. Cartographer writes
`~/.config/crush/crush.json` — the format Crush documents as deprecated — and
not the `crushrc` it now prefers. In the values it writes there, a `${VAR}`
reference becomes `$VAR`, and a value still carrying `$(` is refused rather than
written.

**Why the JSON.** Crush's configuration is Bash: `crushrc` runs at startup, and
`crush.json` is kept "for the forseeable future" as the legacy path. Two of this
repository's invariants pick the file for us. Merging into a client's own MCP
config is non-destructive (D23), which a JSON object supports key by key and a
shell script does not — a managed block in someone's `crushrc` would be a block
of code in the middle of their program, not an entry in their data. And the
second reason is the one that would have been discovered late: whatever
Cartographer writes into `crushrc` *executes* in the user's session on every
start. An MCP entry is configuration; it should not gain the privileges of a
login script by virtue of where it is stored.

The cost is stated rather than hidden: new Crush options are only added to the
Bash format, so a future capability may not be expressible in the file we write.
That is a narrower problem than the one avoided, and it is visible — the JSON
stops describing something, rather than silently doing something else.

**Why the `$VAR` rewrite, and why the refusal.** Crush expands `$VAR` and
`$(command)` in config values at load time; the braced `${VAR}` form that every
other provider receives is not documented. So the emitter rewrites it, exactly as
the OpenCode emitter rewrites the same reference into `{env:VAR}` — one regexp in
the package, two provider vocabularies.

The refusal is the part that is not symmetry. Because values are evaluated, a
header or `env` value containing `$(...)` is a command that runs the next time
Crush starts, and neither Cartographer nor the user would see it happen.
Cartographer's own specs never contain one; a KB-provided `mcp` artifact is
authored elsewhere and can. Emitting it and hoping is not an option, and quietly
escaping it would produce a value that does not mean what its author wrote, so
the emitter fails and names the server and the key.

**Why `agent` and `hook` are unsupported.** Neither Crush's README nor its
`docs/config` page describes a user-level subagent directory or any hook
mechanism. The matrix rule in `internal/provisioning/AGENTS.md` is that a
destination is declared, never inferred: an invented path is a false positive,
which is worse than the miss it was meant to fix. Both cells therefore fail
closed with the reason next to them, as kiro's and hermes' do, and a subagent
artifact lands in `Unsupported` where an operator can see it. If Crush documents
either surface, the cell is a one-line change and every KB gains it at once.

**Why the project scope carries skills only.** `.crush/skills` is one of the
project directories Crush scans by default, so the cell is real. Project
*configuration*, on the other hand, is documented only in `crushrc` form: there
is no documented project JSON filename, and guessing one would mean writing a
file nobody reads while reporting success. The other four project cells fail
closed.

**Consequence worth naming.** Crush's tool namespace is per-server
(`mcp_<server>_<tool>`), so `FlatToolNamespace` stays false and two KBs mounted
without a `tool_prefix` remain reachable — unlike kiro (D102/D144). Nothing in
this change migrates anything: a provider nobody has connected owns no files, so
there is no lockfile transition.
