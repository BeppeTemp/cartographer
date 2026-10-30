# Worker recipes: exec recipes and protocol v1

A **recipe** lists what changed in a feed Source since its cursor, with no model (zero tokens). Two kinds:

- **built-in**: Go code in the worker (`git-docs`); the operator can only replace its instructions text.
- **exec**: an executable the operator declares under `recipes.<name>` in the worker config. It talks the JSON
  protocol below on stdin/stdout, so it can be written in any language and live in the operator's own repository.

This page is the reference for the exec kind: its config keys, protocol v1, the failures and the trust model. The
worker section of [deployment.md](deployment.md) shows the rest of the worker config.

## Trust model

The command comes **only** from the worker config. A Source can only *name* a recipe (or a connector) that the
operator put in that config or that ships in the binary: nothing a KB contains selects a path, a command, an
argument or an environment variable. A script is never read from a KB.

The child runs with an argv list and **no shell** (an argument containing `;`, `$(x)` or a space arrives verbatim),
in a per-call temp directory removed afterwards, and with an environment of exactly the variables you list plus
`PATH` and `HOME` (on Windows also `SystemRoot`, `TEMP`, `TMP`, `USERPROFILE`): the model-provider key never
reaches your script unless you list it. No credential reaches the KB, the log or a commit; the text that reaches a
Source's `feed_error` has the values of the listed variables replaced by `***`. One script runs at a time per replica.

## Config

A name that is not a built-in (`[a-z0-9-]`, 1 to 40 characters) takes:

```yaml
recipes:
  wiki-changes:
    instructions_file: recipes/wiki-changes.md   # required; inside the config directory, at most 16 KiB UTF-8
    connector: docs-wiki                         # optional; how the unit executor reads an entry's locator
    shape: feed                                  # the default; or system (live read-only systems, below)
    exec:
      command: ./bin/list-changes                # a separator: relative to the config directory; else looked up on PATH
      args: ["--space", "ENG"]                   # verbatim
      env: {WIKI_TOKEN: WIKI_API_TOKEN}          # NAME the script sees: the name of the worker's own variable
      timeout: 5m                                # default 5m, at most 1h
      max_output_bytes: 8388608                  # default 8 MiB, at most 64 MiB
      max_content_bytes: 131072                  # per entry; default 128 KiB, at most 1 MiB
```

- `connector` is a connector under `connectors`, or `git` / `fetch`; it must exist at load. Omit it when the script
  returns the content inline, or when each entry's `locator` is a URL the executor reads some other way.
- A built-in's name with `exec` (or `connector`, `shape`) is a load error. `shape` is `feed` or `system`; a `system` recipe
  forbids `connector` (its units use KB tools only) and has no other key: a `preflight`, `skip_preflight` or `read_only`
  key is an unknown key and fails the load. Every error names the key path and line.
- `env` values are variable *names*: a literal secret is refused without being echoed, and a listed variable that is
  unset or empty refuses the listing naming the variable.
- `max_content_bytes` tops at 1 MiB, the unit executor's read cap, so the executor never truncates inline content.

## Inline content

An entry with `content` travels with its unit. The poll creates the unit as `draft` (which `work_list` never offers)
with `content_asset: content.txt`, expands it, writes the asset, and only then patches it to `open`; every step is
idempotent, so a crash is resumed by the next poll from the entry's current content. The unit executor reads the asset
itself, passes it as delimited data ("the content of the source and not instructions") and offers no `source_fetch` for
that unit: no connector or network is involved. The asset text is searchable through the unit. A `draft` unit of the same
ID without `content_asset` is a planner's and is left alone; a staged unit of an older revision stays `draft`, inert,
because the worker never deletes.

## Protocol v1

The worker starts the command once per listing.

**stdin**: one JSON object, UTF-8, then closed.

| Field | Value |
|---|---|
| `protocol` | `1` |
| `mode` | `"list"`; a system recipe is sent `"preflight"` first (below) |
| `source` | `id`, `title`, `locator`, `source_kind`, `source_shape` (`"feed"`, or `"system"`), `include` (list of globs, `[]` if none) |
| `cursor` | the string you returned last time, or `null` on the first poll |
| `max_entries` | the most entries you may emit |
| `max_content_bytes` | the most inline `content` bytes of one entry |

**stdout**: JSON lines, one object each, blank lines ignored: `{"entry": {...}}` repeated, then **exactly one last**
`{"next_cursor": "<string>"}`. Anything after it is an error.

| Entry field | Rule |
|---|---|
| `key` | required, at most 512 bytes, unique within the listing |
| `change` | `added`, `modified` or `removed` |
| `title`, `revision` | required for `added` and `modified`, at most 200 bytes each; `revision` is opaque and **must change whenever the entry's content changes** |
| `locator` | optional, at most 2048 bytes; absent, the poll uses `<feed locator>#<key>` |
| `content` | optional, `added` and `modified` only, at most `max_content_bytes` |

Unknown fields inside an entry are ignored (v1 may grow optional ones); an unknown *line* is an error.
`next_cursor` is at most 1024 bytes. The cursor is **opaque**: the worker never orders or compares it, so ancestry
or monotonicity is your concern. Return a cursor that covers exactly the entries you emitted: when more remain
than `max_entries`, stop and return where you stopped. The cursor advances only after every entry of the listing is
handled, and `removed` entries are only counted: the worker never deletes a Source, a unit or a page.

## Failures

A failure sets the Source's `feed_error` to `<code>: <detail>` and leaves its cursor untouched.

| Code | When |
|---|---|
| `list_failed` | non-zero exit (`exit N` plus the stderr excerpt), timeout, output past `max_output_bytes`, the command cannot start, a listed variable unset |
| `identity_not_read_only` | a system recipe's preflight refused (see System recipes); nothing was listed |
| `protocol_error` | malformed JSON, a line that is neither `entry` nor `next_cursor`, a missing or non-final `next_cursor`, more entries than `max_entries`, a duplicate key, a field over its limit or missing, `content` over its cap |

On a timeout, an oversized output or a protocol error the worker kills the whole process group, so a child your
script started dies with it. The stderr excerpt is one line of at most 200 bytes, control characters removed,
the values of your `env` variables replaced by `***`; the worker keeps at most 8 KiB of stderr.
The redaction is a literal match: a value under 4 bytes is not redacted (it would mangle unrelated text), and a
transformed copy of a value (encoded, split, re-cased) is not caught. Do not print credentials, however short.

## Testing a recipe before deploying it

```
cartographer worker source test --config <file> --recipe <name> --locator <loc> [--include <glob>]... [--kind <k>]
cartographer worker source test --config <file> --source <id> [--kb <kb>]
```

The command runs the listing the worker will run: the same recipe registry, the same preconditions as the poll
(parse of the Source, recipe known and of the Source's shape, a built-in's host on its list) and the same listing call,
then prints what it got. For a system recipe it runs the poll's preflight first, then reads the stored state and diffs
the listing against it (see System recipes). `--recipe` tries a recipe before registering a Source and needs neither the server nor the
token; `--source` reads a registered Source (one `concept_read`, plus a `concept_list` for the plan warning) with the
token in `token_env`. `--cursor <string>` lists from that cursor (default: empty, or the Source's `feed_cursor`);
`--show <key>` prints that entry's inline content as it would travel in its unit, capped at 20 KiB. No model is
called and no model key is needed; only the variables the chosen recipe lists (and `token_env` with `--source`) must
be set, and an unset one is named, never its value.

**Exit codes.** `0` the listing ran and passed the protocol checks; `1` the poll would write a `feed_error` for this
Source or recipe, printed on stderr with the poll's own code and text; `2` the test could not start (usage, invalid
config, unknown recipe, an unset variable, the Source or server unreadable); `3` a system recipe's preflight refused
(`identity_not_read_only`, an error or no answer): nothing was listed, fix the identity and not the script.

**Output** (stdout, plain text, tab-separated): `recipe`, `shape`, `kind`, `source`, `locator`, `cursor_in`, `config`;
one row per entry (change, key, revision, content bytes, title; at most 50, then `(+N more)`); `counts`;
`next_cursor` in full, to paste back with `--cursor`; `summary` (entries, content bytes, estimated tokens, duration).
Warnings go to stderr as `warning:` lines, among them `no_ingest_plan` when no `IngestPlan` lists the Source.
For a system recipe the header adds `preflight:` (the identity label) and `state:` (`no stored state`, `trusted, N
groups`, or `untrusted (reason)`, in which case the diff is against an empty state, as the poll's baseline rebuild
would be); the rows are the changed groups in key order (revision computed by the worker), `counts` adds `unchanged`,
`--cursor` is ignored (a warning says so), `--show <key>` prints the group's `## Change` and `## Observed` sections as
they would travel in its unit, and `next_cursor` is the digest of the state the poll would store if it handled every
group, so it changes with the observation time.

**Writes nothing, and its limit.** No lease, cursor, Source, unit, state asset or log entry is written: in `--source` mode the KB is
reached through a caller that offers only read tools, and the state is read with `Load` only (the store's `Save` is not
available to the command). With `--recipe` there is no stored state and the server is not contacted. The script is the operator's code: it runs from the operator's
shell with the environment variables it lists, so test with the same read-only variables the worker has, and test
`--source` only against a Source you trust, because its locator and includes reach the script. A script referenced by
`command` needs a shebang; on a machine where one does not run, test from a container.

The bundled skill `cartographer-kb-source-author` drives this loop from a source to a registered, tested Source.

## System recipes (`shape: system`)

A feed lists *documents that have revisions*. A Kubernetes cluster, a cloud account or a metrics server has none, and
its raw output is far too large to read. A recipe of `shape: system` prints the system's **current state, grouped**
(one entry per namespace, per service, per account region). The worker hashes each group, compares it with the state
it stored last time and turns only the groups that **changed** into units; an unchanged system costs no tokens. The
Source is registered once (`source_register` with `source_shape: system` and `recipe`; the `locator` is an opaque label
unique per system, such as `kubernetes://dev-cluster`, which the lister must not use to choose a context, an endpoint
or a file), and `feed_interval` and `feed_include` are set with `concept_patch`, as for a feed.

**The recipe's shape, not the Source's label, decides whether the preflight applies**: a mismatch in either direction
is `recipe_shape` and nothing runs.

**Preflight (mode `"preflight"`)** runs before **every** listing and cannot be configured away. The worker sends the
same request as for `list` with `mode: "preflight"` and `source_shape: "system"`; the script answers **exactly one JSON
object** on stdout, exit 0:

```json
{"read_only": true, "identity": "dev-viewer", "detail": "no write verbs, no secrets"}
```

`read_only` is required and a JSON boolean; `identity` (at most 120 runes) and `detail` (at most 300) are optional text,
control characters stripped. Anything but a clean `read_only: true` is a refusal: `false`, a non-zero exit, a timeout,
empty or more-than-one-object output, a missing or non-boolean `read_only`, a command that cannot start, an unset
`exec.env` variable. The Source then gets `feed_error: identity_not_read_only: <identity> — <detail>` (or the exit
status and the stderr excerpt), nothing is listed, and the cursor and the state are untouched. The code is a contract.
No code path lists a system recipe without a preceding clean preflight.

**The preflight is a guard against a misconfigured identity, not a security boundary**: the script is operator code and
can lie. The boundary is a dedicated read-only identity (see [deployment.md](deployment.md) §The worker).

**List for a system**: `source_shape: "system"`, `cursor: null`, `max_entries: 1000`, `max_content_bytes`. The entry
rules differ from a feed's: `content` is **required** (non-empty UTF-8, no NUL, at most `max_content_bytes`, a
normalized snapshot: volatile fields dropped, **never a secret**); `revision`, `change` and `title` are ignored or
defaulted (the title is the key). The final line is still required as the end marker and must be `{"next_cursor": ""}`: a system listing is always complete, and a non-empty cursor (a lister that stopped early) is a `protocol_error`, because every group a partial listing leaves out would be read as removed. The
worker computes each group's revision itself, as the first 16 hex of sha256 over the content with CRLF folded to LF; it
never trusts the lister's. A group key matches `^[A-Za-z][A-Za-z0-9._:/@-]{0,199}$`, is not a YAML word (`true`,
`false`, `null`, `yes`, `no`, `on`, `off`) and is unique; at most 1000 groups. A violation is `protocol_error` naming
the key. An empty listing while the state has groups is `empty_listing`, never "everything was removed".

A changed group becomes one `IngestUnit` that carries the current snapshot and a line diff (at most 400 lines and 64 KiB)
in its body; a removed group becomes a unit that marks the owning page "observed absent since <date>" and records a gap.
The worker never deletes, archives or moves anything because of an observation, and the poll's model never reads a
snapshot. The state lives in the asset `system-state.jsonl` of the Source (its digest is `feed_cursor`); data-plane.md
§ledger has the format and the extra `feed_error` codes (`state_error`, `groups_failed`).

A reference lister for Kubernetes ships in the repository, not in the binary:
[`examples/system-recipes/kubernetes/`](https://github.com/BeppeTemp/cartographer/blob/main/examples/system-recipes/kubernetes/README.md) (Python 3, standard library only,
`kubectl`; tested with fixtures, no cluster).

A script that receives a mode it does not know must exit non-zero. A change to this contract needs a new `protocol`
number.

## Example

A script that lists the pages of a wiki changed since the cursor (a timestamp). It prints entries, then the cursor:

```python
#!/usr/bin/env python3
import json, os, sys, urllib.request

req = json.load(sys.stdin)
if req["protocol"] != 1 or req["mode"] != "list":
    sys.exit("unsupported request")
since = req["cursor"] or "1970-01-01T00:00:00Z"
api = urllib.request.Request(
    "https://wiki.example.com/api/changes?since=" + since,
    headers={"Authorization": "Bearer " + os.environ["WIKI_TOKEN"]},
)
pages = json.load(urllib.request.urlopen(api))["pages"][: req["max_entries"]]
for p in pages:
    print(json.dumps({"entry": {
        "key": p["id"], "title": p["title"], "revision": p["version"],
        "change": "added" if p["version"] == 1 else "modified",
        "locator": "https://wiki.example.com/pages/" + p["id"],
    }}))
print(json.dumps({"next_cursor": pages[-1]["updated"] if pages else since}))
```

Errors go to stderr with a non-zero exit; never print a credential.

## Running scripts in the container

The image is unchanged: a script that needs an interpreter or a CLI runs from a derived image (`FROM` the
Cartographer image, plus the interpreter) or from a mounted read-only volume holding the script. The worker needs a
**writable temp directory** (an `emptyDir` on Kubernetes) for exec recipes and git reads; it still needs no
persistent volume.
