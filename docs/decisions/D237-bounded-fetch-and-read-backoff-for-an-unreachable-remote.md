---
topic: concurrency-git
---

# D237 — A bounded fetch, and a read backoff, for a remote that does not answer

**Decision.** `gitx.Fetch` runs under a 15s deadline and kills git's whole
process group when it expires, returning a `RemoteTimeoutError` that names the
remote. After a failed fetch, read-only tools skip `SyncIn` for 60s and serve the
local clone; the check is repeated under the git lock, so calls queued behind
the failing fetch do not each start their own. Writes are unchanged: they fetch,
and fail, every time. The client words a timeout while awaiting headers as "the
server accepted the request but did not answer in time", not "could not reach".

**Why.** A remote whose host drops packets held the fetch for the OS TCP connect
timeout (~75s on macOS) while the client gives up at 30s, so every `sync` failed
against a KB the server would in the end have served from its clean local clone,
and retries queued on the git lock one full wait each (#348). The fetch is the
operation that meets the unreachable remote first; pull and push run only after a
fetch has just succeeded, so they are left without a deadline rather than risk
killing a rebase halfway. The process group matters because git fetch over http
runs `git-remote-http` as a child: killing only git left one orphan per timeout.
The cost is that a genuinely slow fetch over 15s now fails, and a read in the
60s after a failure may miss a commit that has since become fetchable.

**Alternatives rejected.**
- Raise the client timeout to cover the OS connect timeout: every call on a
  healthy server with a broken remote would still take 75s, and retries still
  compound.
- Skip the fetch when the local clone is clean and even with `origin/<branch>`:
  "even" is only known after a fetch, which is the call that hangs.
- A server-wide `WriteTimeout`: D166 already rejected it, since it truncates a
  slow success instead of bounding the operation.
- A backoff for writes too: a write must not commit on a base it could not
  refresh; failing it is the honest answer.

**Consequences.** Reads against a KB whose remote is down answer within one
fetch deadline, then immediately for 60s. `FetchTimeout` is a package variable
only so tests can shorten it; it is not configuration. A future git network
operation that can hang (pull, push, ls-remote) should use the same
`killGroupOnCancel` shape rather than a bare `CommandContext`.
