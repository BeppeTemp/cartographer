---
topic: architecture
---

# D1940 — the exec recipe protocol has verbs (check, list), a delete line, and no v1

**Decision.** The exec recipe protocol is version 2 and replaces version 1 with no compatibility mode. A request carries
a `verb` (`check` or `list`) and a `config` object, not a `mode`. `check` verifies credentials and reachability and, for a system recipe, absorbs the old preflight (`read_only` is
required there, and `ok: true` with `read_only: true` is the proof `ListSystem` demands); `list` ends with exactly one
`next_cursor`. A deletion is a `{"delete": {"key": ...}}` line, `change: removed` is refused, and a system listing has no
delete lines. A script exits 64 with no output for a verb or a protocol number it does not implement: for
`check` that is "unsupported", for `list` a failure.

**Why.** The Surveyor was never released with v1 (`v0.28.0` has no Surveyor, the changelog no recipe entry), so a
compatibility layer would carry a contract nobody depends on. The one check
the Surveyor could ask (the system preflight) was a special mode; a verb that every recipe may implement gives a feed
the same credential and reachability check without a second mechanism. A deletion as its own line keeps `entry` a
statement about content that exists, so an entry with `content` and `removed` cannot be written.

**Alternatives rejected.** Keeping `mode` and adding values: the request would still be one overloaded shape and v1
scripts would silently keep running against a changed meaning. A per-recipe `skip_check` key: the proof of a read-only
identity must not be configurable away (D1372). Running a check at every feed poll: one more process per poll for a
result only the dry run and the author read; the poll calls the verbs its shape needs.

**Consequences.** `recipe.Verified` now proves a clean `check`, for both shapes; the two failure codes of a system check
are `identity_not_read_only` (the identity is not read-only, or `read_only` is absent) and `check_failed` (everything
else), and `source test` exits 3 for both. `check_failed` is a new `feed_error` code. The verbs a spec declares gate the calls of `source test` only: the poll never runs `spec`
and calls `check` (system) and `list` by contract, so a spec is a description, not a permission. A change to what
is sent or accepted needs a new protocol number (D1262).

**Fetch.** The poll, not the unit executor, asks for the content of the entries that need a new unit (`fetch`, at most 25
keys per call, `{"content": ...}` per key then `{"done": true}`) and stages it as the unit's `content_asset`, the path
inline content already takes. The alternative, letting the model call a connector for it, would carry the source's
credential into the model loop. The poll does not run `spec` to learn whether a recipe has `fetch`: it asks, and exit
64 with no output means "none", costing one process per poll for a recipe that lists without content and has no
`fetch`. A failure (`fetch_failed`, `protocol_error`) ends the poll with the cursor untouched; a staged unit a crash
left without its asset is fetched again, so a resumed poll converges as it does for inline content. A poll fetches at most 8 MiB in total, the default `max_output_bytes`, checked before each batch: one listing's inline content has that bound, and fetched content must not let a poll hold more; the entries past it get units as for a recipe without `fetch`, so the poll still advances. Exit 64 after the script printed anything is a `protocol_error`, not "no fetch": a partial answer must not pass as a missing verb.
