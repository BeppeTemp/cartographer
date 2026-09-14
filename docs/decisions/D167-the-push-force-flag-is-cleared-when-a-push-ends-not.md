---
topic: concurrency-git
---

# D167 — The push force flag is cleared when a push ends, not only when one starts

**Status: implemented (2026-09-05).** Amends [D76](D76-write-path-latency-batch-patch-sync-coalescing.md).

**Context.** `FlushPush` sets `pushForce` to make the worker skip the debounce window, and the worker
clears it when it *starts* a push. Those two do not line up for a flush that arrives while a push is
already running: the in-flight push satisfies the flush's waiter — correctly, that is what the caller
asked to wait for — but it started before the flag was set, so nothing ever consumes it. The flag
then survives into the next, unrelated `SchedulePush`, which pushes immediately instead of
debouncing.

Benign in effect: the push still happens, just earlier than the window intended. It is recorded
because the failure is invisible — one un-debounced push looks exactly like a correctly debounced one
from the outside — and because it is the same class of bug the file's own header comment describes
having already fixed once, where worker state and wake signals disagreed about what had happened.

**Decision.** `pushForce` is also cleared in the post-push critical section, alongside `pushRunning`
and the waiter list, so the flag's lifetime is bounded by the push that satisfies the flush rather
than by the push that consumes the flag.

**Rationale.** The alternative — having `FlushPush` skip setting the flag when `pushRunning` is
already true — was rejected as racy in the direction that matters. `FlushPush` releases `pushMu`
before waiting, so a push it observed as running can finish before the waiter is registered; the
flush would then have neither forced anything nor been satisfied by the in-flight push, and would
wait out a full debounce window it explicitly asked to skip. Clearing late is unconditional and
cannot mistime.

A pending write left behind by the cleared flag is a *later* signal than the flush that set it, so
debouncing it is the correct behavior and not a dropped force.

**Consequences.** The debounce window is honored for every push that is not itself forced.
`pushworker_test.go` gains a regression test that parks the worker inside `doAsyncPush` by holding
the git lock, which makes "the flush arrives mid-push" deterministic rather than a timing gamble.
