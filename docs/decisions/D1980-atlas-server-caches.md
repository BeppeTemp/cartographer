---
topic: control-plane
---

# D1980 — The Atlas routes reuse what the graph cache holds, and lint is paged

**Decision.** The overview counts from the link-graph cache's facets, the graph's
PageRank and communities are memoized per graph generation and per visible set
(the key is a fingerprint of the ids the caller sees), and `GET .../revision`
trusts the last validation of the cache for up to 3 seconds: a write Meridian
makes ends that trust, an out-of-band edit (a pull, an editor) can take up to
3 seconds to move the token. `/api/ui` JSON is gzipped when the client sends
`Accept-Encoding: gzip`, and `GET .../lint` is paged with `limit` (default 1,000,
at most 5,000) and `cursor`: `count` is the total across pages, `next_cursor` is
the offset of the next page in the ordered list and is absent on the last.

**Why.** On KBs of tens of thousands of concepts the Atlas cost was server work
repeated on every page load, KB switch and live refresh (#1962): a read and parse
of every file for the counts, a PageRank and a Louvain pass per graph request, an
lstat walk per open tab per poll, and uncompressed bodies with every lint finding.
Keying the memo by the visible set rather than by the caller keeps D226 and D244:
a principal that sees fewer concepts never gets a ranking computed over more. The
price of the poll window is that the one reader allowed to use an old validation
can lag an external edit by 3 seconds, which a 10-second poll hides.

**Alternatives rejected.**
- Keying the memo by principal: more entries, and two principals with the same
  visible set would not share it.
- Rate-limiting every graph read: breaks D241's rule that a read follows the files.
- A cursor that encodes the last finding's key: stable across changes, but needs
  the lint order's comparator in the cursor; the offset can repeat or skip a
  finding when the list changes between two calls, and the Atlas reloads the whole
  list when the revision moves.
- Moving the Atlas from polling to `/events`: a separate change.

**Consequences.** A new reader of the `/revision` poll must not validate on its
own; a new write path must go through `WithGitLock`, or the poll lags it by up to
3 seconds. A lint consumer must follow `next_cursor`: the unpaged list no longer
exists. The memo must stay keyed by the visible set.
