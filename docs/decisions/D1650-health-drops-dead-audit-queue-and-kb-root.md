---
topic: control-plane
---

# D1650 — `/health` drops the audit queue counters and the empty KB root

**Decision.** The audit state in `/health`, `/ready` and `cartographer status` no longer carries `queue_depth` and `queue_capacity`, `Options.QueueCapacity` is gone, and the KB entries of `/health` no longer carry `root`. Appends are still serialised and fsynced one by one; there is no admission limit.

**Why.** The counters described a limit that could not fire: the depth was incremented and decremented while holding the log mutex, so it was at most 1 and always read 0, and concurrent callers waited on the mutex without bound. `root` was always the empty string. A field that always says the same thing reads as a measurement.

**Alternatives rejected.** Real admission (an atomic counter taken before the lock) would start failing appends under load in required mode with the default capacity of 256: a behaviour change nobody asked for. Filling `root` would publish a server path to every caller that reads `/health`.

**Consequences.** A reader that ignores unknown keys is unaffected; one that required these keys sees them absent. If admission control is wanted later it is a new, tested feature with its own decision, not a revival of the counters.
