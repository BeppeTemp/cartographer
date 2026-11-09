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
unit executor reads a git entry from a clone scoped to that unit's run (one per unit, deleted when it
ends, not shared across units). Every clone of a KB-supplied locator is anonymous
(`gitx.CloneAnonymous`: no git config, credential helper, askpass or extra header of the operator, no
redirect, a timeout, `--filter=blob:none`), because "https, no credential" would otherwise be undone by
the worker's own git setup; KB sync keeps the gitx defaults.

## Exec recipes and protocol v1 (fourth step)

The second kind of recipe is an executable the operator declares under `recipes.<name>.exec` in the worker
config (`internal/worker/recipe/exec.go`, reference `docs/worker-recipes.md`). The invariant is that nothing a KB
contains selects code: the command, its arguments and its environment variables come from the config and a Source
can only name the recipe. The runner uses an argv list with no shell, a per-call temp directory, the environment of
`connector.ChildEnv` (exactly the listed variables plus `PATH`/`HOME`) and the process-group kill of the connector
package, so a timeout, a cancel or an output past `max_output_bytes` takes the script's children with it; stdout is
read as a stream, so a script cannot make the worker hold more than the cap; stderr is kept to 8 KiB and shown as one
200-byte line with the listed variables' values replaced by `***`. The protocol is versioned (`protocol: 1`): v1
sends only `mode: "list"` with `source_shape: "feed"`, and `mode: "preflight"` and `shape: system` are reserved for
live read-only systems (#1372), where `shape: system` is a load error. A change to what is sent or accepted needs a
new protocol number. This amends the earlier rule that the operator's only lever over a recipe is its text, which
holds for built-ins only.

**Alternatives rejected.** A shell for the command (quoting becomes the operator's and a KB-derived argument an
injection); reading the script or its path from a KB (a KB writer would run code on the worker); letting the script
see the worker's whole environment (the model-provider key would reach operator code); an unbounded stdout read
(memory is the script's to exhaust); an ordered or comparable cursor (it is the script's, the worker only stores it).
## The poll task and http-pages (third step)

The poll is stateless: the cursor and the lease are Source fields written with `if_match`, and the
unit ID `<unit map>/<slug>-<8 hex of sha256(feed|locator|revision)>` is the idempotency token, so a
crash anywhere re-lists and converges and a model call is never repeated for an entry that has its
unit. The model call returns data (`targets`, `extract`, `verify`, `executor`, `body`) that the worker
validates and writes; entry text reaches only the user message, as data. A Source whose registration
already exists is treated as a changed document whatever the entry's `change` says, so `http-pages`
(which emits every revision as `added`) reuses the previous unit's fields. `http-pages` reads through
the unit executor's own fetch, behind `fetch.allow_hosts` and https. Every git command run in an
anonymous clone also redirects `HOME` and `XDG_CONFIG_HOME` (`gitx.AnonymousReadEnv`). The unit
executor now starts a declared connector before claiming a unit and offers its allowlisted tools to that
unit's loop alone. A failure detail reaches `feed_error` only after `RedactEnv` of every connector
variable. Alternative rejected: a per-run "attempted feeds" set in the instance, which has no run
boundary to reset on; `feed_checked_at` already makes a feed not due.


**Inline content (the last PR of the series).** An entry's `content` is written as the asset
`content.txt` of its unit (`content_asset: content.txt` in the frontmatter). The unit is staged as
`draft`, which `work_list` lists only where `open_statuses` names it, and opened last: create, expand,
asset, patch to `open`, each idempotent and resumed by the next poll (a `draft` unit with
`content_asset` is ours, one without is a planner's). The executor reads the asset before the loop,
puts it in the prompt as delimited data and offers no `source_fetch` for that unit, so such a unit
needs no connector. Accepted: the asset text is searchable through the unit. Alternatives rejected:
the content in the unit body (it would count against the 8 KiB brief and be rewritten by the loop),
and an `open` unit written first (a claimable unit with no content).
