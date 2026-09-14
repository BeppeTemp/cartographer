---
topic: control-plane
---

# D185 — The read path stops sending the same bytes twice

**Status: implemented.** Closes #240.

**Context.** Measured on a real Kiro session against a 715-concept KB (11.4 MB of Markdown,
median concept 14.5 KB, p90 27 KB), the two content-returning read tools cost more than the
information they carry — and every byte is re-sent to the model on every subsequent round-trip
of the turn.

`concept_read`'s full response built `{id, content, content_hash, frontmatter_raw, body}`, where
`content` is frontmatter+body and `body` is that same body again. One live `tools/call` returned
**37,982 bytes of payload for an 18,429 byte body**: `body` 18,429 B, `content` 18,735 B,
everything else 379 B — a factor of 2.06x on every full read. In the session analysed it was the
single largest consumer of tool-result bytes: 48 calls, 167,359 bytes, roughly 42k tokens.

`index_get` had no size check at all: it returned the raw Markdown verbatim, or wrapped it with a
hash. It was the only content-returning read tool without the protection `concept_read` has had
since [D78](D78-readable-per-path-log-concept-read-with-size-guard-and.md), and it is a hot path rather than an edge case — `concept_list`'s own description
routes the agent to it for progressive navigation. Measured: `index_get('attivita')` returned
**38,587 bytes** (~9.6k tokens) in one call, twice in the same session.

The downstream cost is not only tokens. Above the client's own threshold the host offloads the
payload to a file, and the agent then spends extra `bash` round-trips grepping it — six of them in
one session, each carrying the full accumulated context. An oversized response costs tokens *or*
round-trips; never zero.

**Decision.**

- **`body` is the field that stays; `content` becomes opt-in.** It is what every caller actually
  consumes and what the size guard already measures. `content` is not dropped — a caller that
  must re-write the exact bytes still needs it — but it is requested with `with_content: true`
  rather than paid for by default. `frontmatter_raw` stays unconditional: it is small (297 B in the
  measured case) and it is the only way to read frontmatter without a second call.
- **This is a breaking change in `concept_read`'s default response shape**, and it is acceptable:
  the consumer is an LLM reading a documented tool, not a pinned API client, and
  [D161](D161-every-enforced-limit-and-contract-stated-in-its-own.md) requires the contract to be stated in the description anyway — which it now is.
- **`index_get` reuses `okf.ConceptReadSizeGuard`** (60000) rather than introducing a second
  threshold. [D159](D159-per-concept-lint-opt-out-home-anchored-allow-prefixes.md) already reasons about the relationship between that guard
  and lint's `conceptOversizeThreshold`; a third number would have to be reconciled with both.
  Over the guard it returns `{path, content_hash, outline, content_bytes, note}`, built with the
  same `headingsToOutline(okf.ListHeadings(...))` helper `concept_read` uses. `full: true` forces
  the whole content, `outline: true` asks for the outline at any size — the same field names and
  the same semantics as `concept_read`, deliberately, rather than a second vocabulary for the same
  idea.
- **The guard measures the whole file, frontmatter included.** A curated index's frontmatter is
  three lines; splitting it off would only add a code path.

**Invariants kept.** `concept_read`'s guard branch keeps its trigger condition — it fires only when
neither `section` nor `outline` nor `full` was requested — and the `outline` and `section` responses
are unchanged, including the fact that neither ever carried `content`, so `with_content` is ignored
there rather than quietly extending them. An `index_get` under the guard with no `with_hash` still
returns **byte-for-byte verbatim Markdown**, because `index_patch` callers diff against it.

**Consequences.** A full read of an N-byte concept costs about N plus frontmatter and hash instead
of about 2N. An oversized curated index can no longer be served whole by accident. Tests in
`internal/mcpserver/server_test.go` pin both: that `content` is absent by default and equal to the
file's bytes when asked for, that `with_content` changes nothing in the other branches, and that
`index_get` returns the guard shape, the forced full content, the outline at any size, and
unchanged verbatim Markdown under the guard.
