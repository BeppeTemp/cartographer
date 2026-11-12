# Writing a Surveyor connector

A connector is how a Surveyor reads a source that is not a git repository or a web page: a wiki, a ticket tracker, a
chat archive, a live system. A **plugin** ships it, so an operator pins one digest and every Surveyor runs the same
code ([plugins.md](plugins.md#surveyor-side-recipes-and-connectors)). This page is for the author: what to put in the
folder, which kind to choose, how to write the listing, and how to test it before a user does. The wire contract is in
[surveyor-recipes.md](surveyor-recipes.md); the checks `plugin test` runs are in
[plugins.md](plugins.md#conformance-checks).

## The layout

```
my-plugin/
  cartographer-plugin.yaml       components.surveyor: sdk, permissions.net, permissions.env, settings
  surveyor.star                  Starlark recipes: recipe(name, list, spec=, check=, fetch=)
  recipes/<name>/instructions.md the text the poll's model reads (required, at most 16 KiB)
  recipes/<name>/recipe.yaml     an exec recipe: command, args, shape, limits (absent for a Starlark recipe)
  recipes/<name>/<command>       the program, with the execute bit
  connectors/<name>.yaml         an MCP connector: transport, command or url, the tool allowlist
  tests/<name>.yaml              the scenario of `plugin test` (not stored, not part of the digest)
```

A **recipe** lists what changed in a source: it is what a Source names (`recipe: my-plugin/<name>`). A **connector** is an
MCP server the unit executor calls to read one entry; a recipe's `recipe.yaml` names it with `connector:`. Start with a
recipe: a source with `list` alone already works, and a connector is only needed when reading an entry takes a tool call.

## Exec, Starlark or MCP

| Choose | When | You get | You give up |
|---|---|---|---|
| **Starlark recipe** | The source has an HTTP API and the listing is a few requests | No interpreter in the image, `http` limited to the manifest's `net`, recorded tests, the same parser as exec | No files, no subprocess, no libraries; one script, no `load()` |
| **Exec recipe** | You already have a program, or need a library or a local tool | Any language, a real process, a process group the Surveyor kills on a timeout | The Surveyor image must hold its interpreter; the process reaches whatever the Surveyor's network does |
| **MCP connector** | An MCP server for the source exists and the model should read entries through it | The unit executor calls its allowlisted tools | It has no `list`: pair it with a recipe that lists keys; an http connector is a live server `plugin test` never reaches |

A Starlark and an exec recipe speak the same protocol: the Starlark host writes the dict a function returns as the lines an
exec program prints and reads them with the same parser, so a listing is valid by one definition.

## Writing `list`

`list` is called once per poll and returns the entries that changed since the cursor. Keep it cheap and stable:

- **Cheap.** Return `key`, `title` and `revision` for each entry, and `content` only when it comes with the listing.
  A recipe that lists without content declares `fetch` and gives the content of the entries that need a unit.
- **A stable `revision`.** It is what the Surveyor compares: the same page must give the same revision until it changes.
  Use the source's own version, an `updated` timestamp or a hash of the content, never the time of the call, a counter or
  anything that differs between two runs. A revision that moves every poll costs a model call per entry.
- **A stable `key`.** The key identifies the entry across polls; a renamed page keeps its key when the source has one.
- **Bounded.** Return at most `max_entries` entries per call (the request carries it) and content within
  `max_content_bytes`; the parser refuses more.

## Cursors

The cursor is an opaque string the recipe hands back and the Surveyor returns untouched on the next call (`null` on the
first). It is **resumable**: the Surveyor stores it only after every entry of the listing is handled, so a crash replays
the same listing, and `list` must give the same entries for the same cursor. It is **one page per call**: return a cursor
that covers exactly what you emitted and let the next poll continue; do not loop over the whole source in one call. An
empty `next_cursor` means "nothing more to resume"; a system listing always ends with an empty one.

## Deletes

When the source says an entry is gone, emit `delete` with its key (`deletes` in Starlark). The Surveyor only counts a
delete; it never deletes a concept, so a KB keeps what it learned and a human decides. A key is either an entry or a
delete in one listing, never both.

## `fetch`

`fetch` receives up to 25 keys and answers the content of each. The poll calls it only for entries that need a new unit,
and stores the answer as the unit's content, so the source's credential stays in the recipe's environment and never
enters the model loop. Every key asked is answered, or the poll fails with `protocol_error`.

## Rate limits and retries

The host supplies a timeout (a Starlark verb has 5 minutes; an exec recipe has its `timeout`) and kills what overruns it.
**Retries belong to the recipe**: back off on a 429 or a 5xx inside one call, within the time you have, and fail clearly
when the budget is spent. A failed `list` leaves the cursor where it was and the next poll tries again, so a transient
failure costs a poll, not data. Do not sleep for minutes: the slot is one script at a time per Surveyor.

## Credentials

A plugin never names an operator variable. Declare the variable names the code sees under
`components.surveyor.permissions.env`; the operator maps each to one of the Surveyor's own variables with
`plugins.<name>.env`. In Starlark read one with `env.get("NAME")`; an exec program reads its environment, which holds
those names and `PATH` and `HOME` only. **Never print a credential**: not in an entry, a title, a log or an error. The
Surveyor redacts the mapped values from a failure it reports, but only literally and only for values of 4 bytes or more,
and an entry's text is data it does not rewrite. `plugin test` fails a recipe whose entry or error holds an `env` test
value.

## Private networks

A Starlark recipe reaches only the hosts of `permissions.net`, and never a private, loopback or link-local address. An
internal wiki that resolves to a private address needs two keys: its host under `permissions.net_private` in the
manifest, and the same host under `plugins.<name>.allow_private_hosts` in the Surveyor config. An exec recipe or an MCP
connector process is outside this control: it reaches whatever the Surveyor's network does, and the preview says so.

## Testing with `plugin test`

`cartographer plugin test <dir>` runs each recipe and connector against `tests/<name>.yaml` and the conformance checks.
A Starlark recipe's `http.request` is answered from recorded exchanges, so it touches no network. An exec program and a
stdio connector are **run for real, as you, unapproved and with your network** (the command is printed before it starts),
the exec program with the scenario's `env` and the connector through the same connector set a Surveyor opens; an http
connector is never reached, and its calls are only checked against the allowlist. Record the
exchanges your recipe needs, write the steps you expect, add a second stage so the checks can see an unchanged entry stay
unchanged. The scenario format and the checks are in [plugins.md](plugins.md#testing-a-surveyor-side-plugin-test).

## A worked example

[examples/plugin-recipe](../examples/plugin-recipe) is a Starlark recipe for an example wiki: `surveyor.star` registers
`example-pages` with `spec`, `check` and `list`; `tests/example-pages.yaml` records two stages of the wiki; and
`cartographer plugin test examples/plugin-recipe` passes. Read the three files together: the recipe keeps the token in
the `Authorization` header and nowhere else, uses the page `version` as the revision and the time of the last page as
the cursor.

## What a connector cannot do

**The KB has no per-concept access control, so a source's permissions are not synced.** Whatever the identity the
connector uses can read ends up as KB text readable by every reader of that KB: a page restricted to three people in
the wiki becomes a concept every reader of the KB can open. Nothing in a Source or an entry carries an ACL, and nothing
in the Surveyor filters by the reader.

So:

- use a **dedicated read-only identity** limited to what all readers of the KB may see, not a person's token or an
  administrator's;
- keep audiences in **separate KBs**: a KB is the trust domain, and a source whose pages differ by audience belongs to
  one KB per audience, each with its own Source and identity;
- narrow what is read with `feed_include` on the Source (a path or space glob) so the identity is not the only fence;
- treat what the identity can read as published to the KB's readers when you grant it.
