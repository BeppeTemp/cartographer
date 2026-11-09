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
    shape: feed                                  # the default; system is refused (reserved for live read-only systems, #1372)
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
- A built-in's name with `exec` (or `connector`, `shape`) is a load error. Every error names the key path and line.
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
| `mode` | `"list"`; the worker sends nothing else in v1 |
| `source` | `id`, `title`, `locator`, `source_kind`, `source_shape` (`"feed"`), `include` (list of globs, `[]` if none) |
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
| `protocol_error` | malformed JSON, a line that is neither `entry` nor `next_cursor`, a missing or non-final `next_cursor`, more entries than `max_entries`, a duplicate key, a field over its limit or missing, `content` over its cap |

On a timeout, an oversized output or a protocol error the worker kills the whole process group, so a child your
script started dies with it. The stderr excerpt is one line of at most 200 bytes, control characters removed,
the values of your `env` variables replaced by `***`; the worker keeps at most 8 KiB of stderr.
The redaction is a literal match: a value under 4 bytes is not redacted (it would mangle unrelated text), and a
transformed copy of a value (encoded, split, re-cased) is not caught. Do not print credentials, however short.

## Reserved

`mode: "preflight"` and `shape: system` belong to live read-only systems (#1372) and are **not** implemented:
`shape: system` is refused at load, v1 never sends another mode, and a script that receives any mode but `list`
must exit non-zero. A change to this contract needs a new `protocol` number.

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
