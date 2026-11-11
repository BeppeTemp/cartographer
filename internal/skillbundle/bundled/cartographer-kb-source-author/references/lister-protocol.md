# Lister protocol v2 and the recipe config

The full contract is `docs/surveyor-recipes.md` in the Cartographer repository; this is what a lister author needs.

## The request (stdin)

One JSON object, then closed:

- `protocol`: `2`; `verb`: `"list"`, and optionally `"spec"` (describe yourself) and `"check"` (can the credentials
  reach the source?); a system recipe must implement `"check"`. A script receiving another protocol number or a verb it
  does not implement exits 64 with no output.
- `config`: the operator's `recipes.<name>.settings` (scalars; `{}` if none).
- `source` (`check`, `list`): `id`, `title`, `locator`, `source_kind`, `source_shape`, `include` (list of globs, `[]` if none).
- `cursor` (`list`): the string returned last time, `null` on the first listing; `max_entries`, `max_content_bytes`:
  limits the script keeps to.

## The answers (stdout)

`spec`: exactly one line `{"spec": {"title", "description", "shapes": ["feed"], "verbs": ["spec", "check", "list"],
"settings": {name: {"type": "string|int|number|bool", "default", "description", "required"}}, "env": ["NAME"]}}`.
`verbs` must include `list`.

`check`: exactly one line `{"check": {"ok": true, "detail": "...", "identity": "...", "read_only": true}}`;
`ok: false` (or a non-zero exit) fails the check with `detail` shown. `read_only` is required for a system recipe.

`list`: JSON lines, `{"entry": {...}}` and `{"delete": {"key": "..."}}` repeated, then **exactly one last**
`{"next_cursor": "<string>"}` (at most 1024 bytes, opaque: the Surveyor never orders it). An entry has:

- `key` (required, at most 512 bytes, unique, and not also deleted in the same listing), `change` (`added` or
  `modified`; a deletion is a `delete` line, `removed` is refused);
- `title` and `revision` (at most 200 bytes each); `revision` changes whenever the content does;
- `locator` (optional, what the unit executor fetches; absent it is `<feed locator>#<key>`);
- `content` (optional inline text, at most `max_content_bytes`).

Return a cursor that covers exactly what you emitted. Delete lines are only counted: the Surveyor deletes nothing.

`fetch` (optional, declare it in `spec.verbs`): when the listing is cheap and carries no `content`, the Surveyor sends
`{"protocol": 2, "verb": "fetch", "config", "source", "keys": [...], "max_content_bytes": N}` (at most 25 keys) for the
entries that need a new unit. Answer one `{"content": {"key": "...", "content": "..."}}` line per key, non-empty UTF-8
within the cap, then exactly one `{"done": true}`. An unasked, repeated or missing key is `protocol_error`; a non-zero
exit is `fetch_failed`; either ends the poll with the cursor untouched. The credential stays in your environment.
A script without `fetch` must exit 64 with no output on that verb (any other exit fails every poll with
`fetch_failed`); exit 64 after part of an answer is a `protocol_error`. The Surveyor fetches at most 8 MiB per poll.

## System recipes (`shape: system`)

The recipe sets `shape: system` and has no `connector`. Before every listing the Surveyor sends `verb: "check"`; the
script answers `{"check": {"ok": true, "read_only": true, "identity": "...", "detail": "..."}}`, `read_only` true only
if the identity can neither write nor read secrets (ask the system, do not echo a label). `read_only` false or
missing is `identity_not_read_only`; any other failure is `check_failed`. The listing then has `cursor: null`: one
entry per group with required `content` (a normalized snapshot, volatile fields dropped, never a secret), the key a
stable group name, no `delete` lines (a group that is gone is absent) and the final line `{"next_cursor": ""}`. The
Surveyor computes each revision itself and diffs against the stored state. Endpoint and identity come from
`exec.env`, never from the `locator`, which is only a label. Any failure: a message on stderr (no secret) and a
non-zero exit.

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
    settings: {space: ENG}   # optional scalars, sent as `config`; `source test` checks them against the spec
```

- `command` with a path separator is resolved against the config file's directory; it needs a shebang and the
  executable bit. `args` are verbatim (no shell). `env` maps the name the script sees to the **name** of the Surveyor's
  own variable. `timeout`, `max_output_bytes` and `max_content_bytes` have defaults.
- `instructions_file` is required. `connector` is omitted when entries carry `content` or a locator the executor
  reads some other way.

## Skeleton (Python 3, standard library)

```python
#!/usr/bin/env python3
import json, os, sys

req = json.load(sys.stdin)
if req.get("protocol") != 2:
    sys.exit(64)  # a protocol this script does not speak
verb = req.get("verb")
if verb == "spec":
    print(json.dumps({"spec": {"title": "Wiki changes", "description": "Pages of a wiki changed since the cursor.",
                               "shapes": ["feed"], "verbs": ["spec", "check", "list"],
                               "settings": {}, "env": ["WIKI_TOKEN"]}}))
    sys.exit(0)
if verb == "check":  # can the credentials reach the source? a system recipe also proves read_only here
    print(json.dumps({"check": {"ok": True, "read_only": False, "identity": "TODO", "detail": "replace with a real check"}}))
    sys.exit(0)
if verb != "list":
    sys.exit(64)  # a verb this script does not implement
since = req["cursor"] or ""
items = []  # replace: call the source's read-only API with os.environ["WIKI_TOKEN"], items newer than `since`
for it in items[: req["max_entries"]]:
    print(json.dumps({"entry": {"key": it["id"], "title": it["title"],
                                "revision": it["version"], "change": "modified"}}))
# a page deleted at the source: print(json.dumps({"delete": {"key": page_id}}))
print(json.dumps({"next_cursor": items[-1]["updated"] if items else since}))
```
