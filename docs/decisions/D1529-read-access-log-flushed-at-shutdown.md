---
topic: control-plane
---

# D1529 — The read-access log is flushed at clean shutdown

**Decision.** The read-access log is written when a record finds the file older than five minutes (D322) and, new, once at a clean stop: `serveHTTP` after the listener has drained, `serveStdio` after the session ends, through one `MultiKBServer.FlushReadAccess` that calls `save()` on every mounted KB. This supersedes the sentence of D322 "a crash loses at most that window" for a clean stop only: a crash still loses the window. Ids of concepts that no longer exist are pruned at startup, and the file is written outside the log's mutex.

**Why.** The only caller of `save()` was a test. A stdio session shorter than five minutes never wrote anything, a migrated legacy file stayed in memory, and `since` restarted at every launch, so the signal the log exists for could not accumulate.

**Alternatives rejected.** A flush goroutine or timer: D322 rejected it, the lifecycle costs more than the window it closes. Flushing on every record: one file write per read. A separate unmount hook: the server has no in-process unmount (`kb unmount` edits the configuration and restarts the server), so the shutdown flush is that path.

**Consequences.** A new way to stop the server must go through the same shutdown to keep its reads. The log is still per process: two processes on one KB overwrite each other, which is not addressed here.
