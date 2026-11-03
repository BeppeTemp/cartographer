---
topic: control-plane
---

# D1516 — A panic in a tool call is an error result, not a process exit

**Decision.** `callToolChecked`, the one path every tool call takes (the SDK handler and the routed `callTool`), recovers a panic raised in the authorization, the handler, the doctor nudge or the audit close. The caller gets `errorResult("internal error: tool <name> panicked")`, the stack goes to the log, and an audit call already opened is closed with the internal-error outcome.

**Why.** The go-sdk runs each request in its own goroutine with no recover, so one panic in any handler ended the server and every KB and agent on it. `okf.SplitFrontmatter` on a file of exactly `---` was such a panic reachable from KB content.

**Alternatives rejected.** A recover in each handler: forgotten by the next tool added. A recover in the HTTP middleware: stdio and the routed mount do not go through it. Letting the process restart under its supervisor: it drops every in-flight call of every other client.

**Consequences.** A handler may still hold a lock while it panics only through `defer`; `KB.WithGitLock` does, and a test pins that the next write proceeds. A panic is now a result, so it must be read in the log (stack) and the audit trail (`internal_error`), not as a crash.
