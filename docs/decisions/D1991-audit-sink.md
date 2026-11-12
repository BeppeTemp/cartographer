---
topic: control-plane
---

# D1991 — The audit chain is delivered to a sink, from a cursor that is the chain hash

**Decision.** An `audit_sink` hook (D1957) receives the audit chain in chain order, at least
once, in batches, from a cursor that is the hash of the last entry it acknowledged, persisted
atomically beside the log. The log only signals a wake-up after an append (a non-blocking send)
and offers `EventsAfter(hash, limit)`, a read-only walk of the retained segments and the active
file; delivery runs in its own goroutine per sink. `blocking: true` makes a call wait, after its
attempt event and before the tool runs, for the hook to acknowledge it; a `fail_closed` failure
then refuses the call and closes the pair with `audit_unavailable`.

**Why.** An enterprise wants the trail outside the host, without the sink being able to slow or
lose it. The chain hash is unique, ordered and verifiable, so it is a cursor the receiver can check
too. The brief asked for a sink "replayable from `changes_since`"; that view is a git log of concept
changes and knows nothing of reads, denials and refused attempts, which are most of the chain, so
the sink has its own cursor: the two streams share the idea of a resumable cursor, not the source.
Non-blocking by default because the local chain is the source of truth: a failing sink retries with
a backoff (1 s to 60 s) and loses nothing.

**Alternatives rejected.** A cursor by timestamp or line number: not unique across rotation, and a
deleted segment shifts it. Delivering from inside `AppendEvent`: every append would wait on the
network. Exactly-once delivery: it needs a two-phase protocol with the receiver; the entry's `id`
lets it deduplicate. Making blocking the default: it is Vault's blocked-audit-device footgun, an
availability trade the operator chooses with eyes open.

**Consequences.** The canonical audit entry and the hashes of existing records are untouched: the
sink only reads. A cursor whose entries retention deleted, or that is not a hash of this log, makes
the next batch carry `gap: true` and start at the oldest retained entry. `EventsAfter` holds the
append mutex only to snapshot the index and the active size, and starts over when a rotation
happens during its read. `cartographer audit export` stays the offline path. An `audit_sink` on a
server without an audit log stops the start.
