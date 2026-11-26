---
topic: architecture
---

# D2387 — A failed model call is a feed error with a short retry

**Decision.** A model call of the poll that fails (an error that is neither a cancellation of the run nor, for the retry,
a 401 or 403) is written to the feed as `feed_error: model_failed: <entry>: <detail>`, like every other poll failure, and
the Source also gets `feed_retry_after`, `pollRetryBackoff` (15 minutes) after the failure. A feed is due when its
interval has passed or when `feed_retry_after` is later than `feed_checked_at` and has come. The model call runs before
`source_register`: the document's Source is registered just before the unit that names it, and an earlier Source of the
same locator is found with `concept_list` before the call, to reuse its latest unit.

**Why.** The failure used to leave the poll with a bare error: nothing on the Source said why the feed stalled, the
feed waited a full interval (a day by default) for what may be a blip, and the Source registered before the call stayed
without a unit, for a later poll to register again when the entry's key carries a timestamp. Failing through the same
path as `unit_refused` and `grant_exhausted` gives the Atlas feeds view and the Work page the line they already show.

**Alternatives rejected.**
- *`Fail` writes `feed_checked_at = now - interval + backoff`*: no new field, but `feed_checked_at` would then say a
  time the feed was not checked, and the Atlas shows it.
- *A retry on every failure*: a model that stays down would write to the Source every few minutes; only the first
  failure of a streak (the Source does not yet carry the code) asks for it, and the next try is a full interval away.
- *A retry after a 401 or 403*: the credential does not heal in minutes, the runner turns the profile off for the run,
  and the error is still written.
- *Adopting a pending Source with the same feed and locator after the call*: a key with a timestamp defeats it, which
  is why the call goes first. Recipe keys should be stable either way.
- *Reading `feed_retry_after` with `QueryFields`*: the 8-key cap of `concept_list` is full, so the poll reads it with a
  second list; its failure only costs the shorter wait.

**Consequences.**
- `feed_retry_after` is optional, RFC 3339, removed by a success (`Advance`) and by a failure that asks for none; a value
  that is not RFC 3339 is ignored, never `bad_field`, since it only shortens a wait.
- The lease moves `feed_checked_at` past it, so a retry is spent by being tried and never leaves two replicas polling.
- A model failure still returns the error to the runner, which counts it and turns a profile off on `ErrModelAuth`.
- The system poll's model call (one per changed group) takes the same path: `model_failed` with the group's key as the entry and the short retry; it is the `feed_error` the Source carries, and the reasons of groups that failed earlier in that poll are not written.
