---
topic: control-plane
---

# D2436 — The Atlas diff route returns a concept's text before and after a time span

**Decision.** `GET /api/ui/v1/kbs/{kb}/diff?id=&from=&to=` answers, per changed file, the text at the newest revision strictly before `from` and at the newest revision at or before `to`, and the client computes and renders the line diff.

**Why.** The answer is per file because a concept can be a directory (index.md, satellites, assets). The Activity page's rows carry times (a change's `at`, a Surveyor run's `started_at` and duration), not commits, so the span is the natural key. The revisions are picked through `concept_history`, called as the caller, so its guard (renames, redacted subjects) applies once, in the tool; the file contents are read from git at those shas (`gitx.TreeBlobsUnder`, `gitx.BlobPrefix`, restricted to the concept's footprint, a blob never loaded past the cap), and visibility is enforced per file with `Visible`: a satellite by its own concept id, an asset by the concept whose folder holds it, and a map's index.md owns no folder. So a token narrowed by `types:` sees no satellite of another type, and the UI cannot disagree with the agents. Returning texts keeps the server free of a diff algorithm and of a format the client would have to parse; it costs up to two texts per response, capped at 256 KiB each.

**Alternatives rejected.**
- A server-computed unified diff: a second renderer to keep in step with the UI, and it hides the full text the viewer may want to show.
- Two shas as parameters: the Activity rows would first need a lookup from time to commit, a second round trip per click.
- Taking the contents from `concept_read` with `rev`: it reads one concept at a time, so the satellites and assets of a folder would each need their own call and a pairing of paths between the two trees.
- Listing the footprint without a per-file check: the main id is authorized, but a satellite is a concept of its own and may be of a type the caller cannot read.

**Consequences.** The route reads only committed revisions: an uncommitted edit is not in the diff. A concept with more than 100 revisions reports `truncated`. A KB without git answers the current text with `note: no git history`. A concept the caller cannot see is a `404`, as for `/history`. A revision is a commit that touched the concept's main file (the history `concept_history` follows): a span in which only a satellite or an asset changed shows no change.
