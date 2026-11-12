---
topic: control-plane
---

# D1981 — `/health` carries a KB's push state, to whole-KB readers, as classified text

**Decision.** Each KB entry of an identified caller's `/health` has an optional `push` object, `{push_state, unpushed_commits, remedy}`, present only while the push state is `failed` or the oldest unpushed commit (server committer time) is at least 3 minutes old (`kb.PushLagThreshold`). It is given only to a principal that passes the check of `sync_status` (admin, or `AllowsWholeKB` on the KB), not to every principal that sees the KB in the roster. `remedy` is a fixed sentence chosen by classifying `last_error` (`kb.Remedy`), never git's output. `status`, `doctor` and the tray read it. A reading is reused for 15 seconds per KB.

**Why.** Failed pushes piled up in the clone while only agents saw `kb_status`; the humans' surfaces already read `/health`. The roster is visible to any read grant, including a selector-narrowed one, while replication state is whole-KB data, so the field follows `sync_status`, not the roster. Git and forge output carries remote lines, URLs with userinfo and hosts, which must not go out on a public-shaped endpoint. `/health` is polled, and each reading costs git subprocesses, hence the cache.

**Alternatives rejected.** The first line of `last_error` with control characters dropped (leaks raw git text). Redacting URLs and tokens from that line (a deny-list that misses the next format). Showing the field to every roster-visible KB (reveals replication state to a narrowed token). Reading the push state on every request (multiplies git calls under polling).

**Consequences.** The operator does not get the specific cause on `/health`, only the classified sentence: the specific text stays in `kb_status` and `sync_status`. A new failure class that deserves its own sentence is a case in `kb.Remedy`; anything unclassified reads "the cause is in the server's log". A transient state can lag by up to the cache TTL. The field is additive: a client that predates it ignores it, and `client.HealthKB.Push` is nil for a server that predates it.
