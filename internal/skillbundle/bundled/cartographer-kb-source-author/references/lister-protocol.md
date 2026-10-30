# Lister protocol v1 and the recipe config

The full contract is `docs/worker-recipes.md` in the Cartographer repository; this is what a lister author needs.

## The request (stdin)

One JSON object, then closed:

- `protocol`: `1`; `mode`: `"list"`, or `"preflight"` for a system recipe; a script receiving any other mode exits
  non-zero.
- `source`: `id`, `title`, `locator`, `source_kind`, `source_shape`, `include` (list of globs, `[]` if none).
- `cursor`: the string returned last time, `null` on the first listing.
- `max_entries`, `max_content_bytes`: limits the script keeps to.

## The answer (stdout)

JSON lines: `{"entry": {...}}` repeated, then **exactly one last** `{"next_cursor": "<string>"}` (at most 1024
bytes, opaque: the worker never orders it). An entry has:

- `key` (required, at most 512 bytes, unique), `change` (`added`, `modified` or `removed`);
- `title` and `revision` for `added` and `modified` (at most 200 bytes each); `revision` changes whenever the
  content does;
- `locator` (optional, what the unit executor fetches; absent it is `<feed locator>#<key>`);
- `content` (optional inline text, at most `max_content_bytes`, not on `removed`).

Return a cursor that covers exactly what you emitted.

## System recipes (`shape: system`)

The recipe sets `shape: system` and has no `connector`. Before every listing the worker sends `mode: "preflight"`;
the script answers exactly one JSON object, exit 0: `{"read_only": true, "identity": "...", "detail": "..."}`, true
only if the identity can neither write nor read secrets (ask the system, do not echo a label). Anything else refuses
the run. The listing then has `cursor: null`: one entry per group with required `content` (a normalized snapshot, volatile
fields dropped, never a secret), the key a stable group name, and the final line `{"next_cursor": ""}`. The worker
computes each revision itself and diffs against the stored state. Endpoint and identity come from `exec.env`, never
from the `locator`, which is only a label. Any failure: a message on stderr (no secret) and a non-zero
exit. `removed` entries are only counted: the worker deletes nothing.

## Config: `recipes.<name>`

```yaml
recipes:
  wiki-changes:
    instructions_file: recipes/wiki-changes.md
    connector: docs-wiki
    exec:
      command: ./recipes/wiki-changes.py
      args: ["--space", "ENG"]
      env: {WIKI_TOKEN: WIKI_API_TOKEN}
      timeout: 5m
```

- `command` with a path separator is resolved against the config file's directory; it needs a shebang and the
  executable bit. `args` are verbatim (no shell). `env` maps the name the script sees to the **name** of the worker's
  own variable. `timeout`, `max_output_bytes` and `max_content_bytes` have defaults.
- `instructions_file` is required. `connector` is omitted when entries carry `content` or a locator the executor
  reads some other way.

## Skeleton (Python 3, standard library)

```python
#!/usr/bin/env python3
import json, os, sys

req = json.load(sys.stdin)
if req.get("protocol") != 1 or req.get("mode") != "list":
    sys.exit("unsupported request")
since = req["cursor"] or ""
items = []  # replace: call the source's read-only API with os.environ["WIKI_TOKEN"], items newer than `since`
for it in items[: req["max_entries"]]:
    print(json.dumps({"entry": {"key": it["id"], "title": it["title"],
                                "revision": it["version"], "change": "modified"}}))
print(json.dumps({"next_cursor": items[-1]["updated"] if items else since}))
```
