---
topic: architecture
---

# D1262 — Feed sources: connectors with an allowlist, the cursor in the Source

**Decision.** The worker follows a changing source with a cursor kept in the Source
concept itself (`feed_cursor`, `feed_checked_at`, `feed_interval`, `feed_include`,
`feed_error`), written only with `if_match`; and it reads documents through external MCP
servers (connectors) declared in its config, of which the model is offered only the tools
the operator lists, under the name `<connector>__<tool>`. Credentials are the names of
environment variables, never values. This record covers the first PR of the series
(connectors and the cursor fields); recipes, the poll task, exec recipes (protocol v1) and
inline content as a `draft`-staged asset follow in the later PRs of the same plan and will
extend it. Their rules are fixed by the plan and recorded here when they land: a unit's ID
is `<unit map>/<slug>-<8 hex of sha256(feed | locator | revision)>` and the unit is the
idempotency token; listing needs no model; nothing a KB contains selects code, a command, an
argument or a path.

**Why.** The worker is stateless (it has no volume), so the only place a cursor survives a
restart and is visible to every replica is the KB. A compare-and-swap on the Source's own
content hash is then also the lease between replicas: the loser of a `stale_write` skips the
feed silently. A connector's tool list and descriptions come from a third-party server, which
is untrusted text the model reads, and MCP's `readOnlyHint` is advisory and set by that same
server; only the operator knows which tools are reads, hence a per-connector allowlist, deny
by default, enforced on the call as well as on the offer. A child process gets only the
variables listed for it plus `PATH`/`HOME`, so the model-provider key is not exposed to
third-party code. It costs the operator an explicit `tools` list per connector.

**Alternatives rejected.**
- Offer every tool a connector lists: the list is the server's own claim.
- Treat `readOnlyHint` as the filter: advisory, set by the server being filtered.
- Cursor in local worker state: lost with the pod, invisible to the other replicas.
- Cursor in a separate asset or the body: `concept_patch` merges frontmatter under
  `if_match` in one commit and `concept_list` returns it, so the poll needs no per-feed read.
- Values in the config (`env: {TOKEN: abc}`): the loader accepts only variable names, so a
  literal secret written by mistake is refused without echoing it.
- Reuse `internal/gitx`'s process-group kill: it is private to git; the worker keeps a
  copy in `internal/worker/connector` and both comments name each other.

**Consequences.** `connectors` is the first name-keyed mapping of the worker config: the key
table spells its keys `connectors.{name}.<key>`, which the worker loader understands
(`settingschema`'s realign does not, and a worker file is never realigned). No KB tool name
may contain `__` (a test pins it), or routing a connector call by prefix could shadow a
server tool. A connector that cannot start is skipped for the run, not fatal. The unit
executor that offers these tools to the model is a different plan (the unit-execution task);
until it lands the package is complete and tested but not yet wired.

## Recipes and the git reader (second step)

A recipe is Go code shipped in the worker (`internal/worker/recipe`); the only thing the operator
can change on a built-in is its instructions text, through `recipes.<name>.instructions_file`
(config directory, 16 KiB, read at load). The built-in git reader is deny-by-default
(`git.allow_hosts`), https only and without credentials, because a Source `locator` is KB text
written by anyone with write access and must not aim the worker at the internal network; a
private repository is an exec recipe or a connector. The `git-docs` cursor is a bare commit or
`<from>..<to>@<path>` (a listing in progress keeps its end commit); a cursor that is not an
ancestor of HEAD is `cursor_invalid` and is never moved backwards. `gitx.TreeBlobs` and
`gitx.DiffTree` read with `-z` because `DiffNameStatus` mangles spaces and non-ASCII paths. The
unit executor reads a git entry from a clone scoped to the unit's run, not shared across units.
