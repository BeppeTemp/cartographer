# Authoring the KB's artifacts

A KB is not only what an agent reads — it is also **how that agent is set up to
work**. Skills, subagents, hooks, MCP descriptors and standing instructions are
content of the KB, authored with the `artifact_*` tools and materialized into
every connected client's native format.

This is different from `skill_install`, which copies a **bundled** skill (one
shipped inside the binary) into the KB. That is installation, not authoring.

## 1. The six accepted paths

`artifact_write` accepts exactly these, relative to the KB root — anything else
is refused:

| Path | Kind | Shape |
|---|---|---|
| `skills/<slug>/**` | skill | a directory; `SKILL.md` is required, `scripts/` and `assets/` optional |
| `agents/<slug>.md` | subagent | one Markdown file, YAML frontmatter + prompt body |
| `hooks/**` | hook | a directory; `hook.json` plus the script it runs |
| `mcp/<slug>.json` | MCP server | one JSON descriptor |
| `instructions.md` | instructions | one Markdown file at the KB root |
| `templates/<slug>.md` | template | KB-only (D109): never materialized into a client |

## 2. The minimum shape of each

### `skills/<slug>/SKILL.md`

```markdown
---
name: my-skill
description: One sentence saying when an agent should load this skill.
---
# My Skill

Procedure the agent follows.
```

Frontmatter `name` and `description` are both required, and `name` must equal the
directory name. Auxiliary files travel as raw bytes with their executable bit
preserved, so `skills/my-skill/scripts/run.sh` stays executable in the client.

### `agents/<slug>.md`

```markdown
---
name: my-agent
description: When the main agent should delegate to this one.
---
You are …
```

Both frontmatter fields are **required** — `artifact_write` refuses the file
without them. The body is the agent's prompt and travels verbatim. This one
source file is translated per provider: Markdown for Claude Code and Antigravity,
TOML for Codex, an OpenCode agent file for OpenCode. `tools` and `model` keys are
dropped in the translations that cannot carry them, because those names are not
portable between clients. An agent with **no** frontmatter description falls back
to the artifact's own name, which makes it nearly unselectable — write a real one.

### `hooks/<name>/hook.json`

```json
{
  "event": "SessionStart",
  "matcher": "Write|Edit",
  "command": "./on-session-start.sh"
}
```

`event` uses the Claude Code event names as the source vocabulary and is
translated per provider; `matcher` is optional; `command` is resolved relative to
the hook's own materialized directory unless it is absolute — so ship the script
beside the `hook.json` and reference it relatively. An event with no equivalent on
a given provider leaves the hook materialized and produces a **warning, not a
failure**: expect that on a heterogeneous machine.

### `mcp/<slug>.json`

```json
{
  "type": "http",
  "url": "https://example.com/mcp",
  "headers": {"Authorization": "Bearer ${EXAMPLE_TOKEN}"}
}
```

`type` is `http` (needs an absolute `url`, rejects `command`/`args`) or `stdio`
(needs `command`, rejects `url`/`headers`). Every `headers`/`env` value **must**
contain at least one `${VAR}` reference: a value with none is rejected as a
probable hardcoded secret. The client resolves the reference against its own
environment at apply time, so no credential ever lives in the KB. Unknown JSON
fields are rejected.

### `instructions.md`

Free-form orchestration directives at the KB root. Its body is folded into the
generated instructions artifact after the auto-generated archives and agent
sections (D61) — it augments them, it does not replace them.

## 3. Naming rules

These apply to a skill's `name` and directory, and they are the **intersection of
what the supported clients accept**, not the most permissive union (D191). A skill
Cartographer accepts and a client silently ignores is a no-op in the agent's
catalogue, and that failure is invisible from the KB:

| Rule | Severity |
|---|---|
| `name` is required | error |
| `name` is `[a-z0-9]` segments joined by single `-` — no uppercase, no `_`, no leading/trailing `-`, no `--` | error |
| `name` is at most 64 characters | error |
| `name` equals the directory name | error |
| `description` is required | error |
| `description` is at most 1024 characters | warning |
| body is at most 500 lines | warning |

An error excludes **that skill** from the manifest and nothing else: the KB's
other artifacts still sync, and the reason names the KB, the skill and the rule.
Warnings exclude nothing.

The historical `<namespace>--<skill-name>` directory convention is retired — it
is incompatible with the `--` rule above.

## 4. The write protocol

`artifact_write` is optimistic-concurrency, and the rule is inverted between
create and update. Getting it wrong is the first thing that happens to everyone:

- **Creating a new file** — *omit* `if_match`. Passing it on a file that does not
  exist fails with `stale_write: <path> not found`; and if the file *does* already
  exist, omitting it fails with `already_exists: <path> already exists (sha256 …)`.
- **Updating an existing file** — `if_match` is **required**, and its value is the
  sha256 of the current content, taken from `artifact_read` or `artifact_list`.
  A missing or mismatched hash fails with `stale_write`.

So the loop is: `artifact_list` (or `artifact_read`) → take the sha256 → write
with `if_match`. On `already_exists`, read the file and retry as an update. On
`stale_write` for a file you believe exists, someone changed it since your read:
read it again, reconcile, retry.

`artifact_delete` always requires `if_match`, and removes the artifact's own
now-empty directory for skills and hooks.

## 5. What happens after the write

`artifact_write` alone changes **nothing** on any client's disk. The artifact
reaches an agent through a chain, and each link can stop it:

1. **Manifest** — the server scans `skills/`, `agents/`, `hooks/`, `mcp/` and
   `instructions.md` and builds the list of artifacts the KB offers. A skill that
   fails a naming *error* is excluded here.
2. **Binding** — the client must be bound to this KB. On a server with two or
   more KBs, `connect` requires the choice explicitly (D190).
3. **Trust** — a KB artifact needs the user's persisted trust decision. Skills
   and hooks execute with the agent's privileges; bundled artifacts are trusted by
   construction, KB ones are not.
4. **Materialization** — `cartographer sync` writes the translated artifact into
   the client's native directory.

So after authoring: run `cartographer sync`, then **restart the agent session** —
skills and MCP tools are loaded at session start.

A materialized `SKILL.md` carries a provenance block naming the source KB, its
path there, and its content hash. **Editing the materialized copy is not a
channel**: the next sync replaces it. The supported channels are `artifact_write`
on the owning KB, or a git push to the KB repository — the block states which,
with the exact path.

## 6. The rest of the loop

- `artifact_list` — inventory, with the sha256 of every file. Start here.
- `artifact_read` — one file's content plus its sha256.
- `artifact_write` — create or update, per §4.
- `artifact_delete` — remove, `if_match` required.

Full reference: `docs/skills-services-secrets.md` (skills, services, secrets) and
`docs/sync.md` (manifest, trust, pruning).
