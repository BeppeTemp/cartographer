---
topic: concurrency-git
---

# D1614 — A remote URL is redacted where its error is built, by `gitx`

**Decision.** `RedactRemoteURL` lives in `internal/gitx` (it moved from `internal/kb`), and every message that names a remote, whether an error built in `gitx`, a log line, a CLI message or a stored row such as a KB's `origin`, uses its redacted form. `RedactRemoteText` scrubs the raw URL, and the same URL without its userinfo, from git's own output, including the stderr progress `Clone` shows live.

**Why.** The error text of a clone or probe reaches the server log and an agent transcript, so a `https://user:token@host/...` remote must not be in it. Redacting at the display site left every new message one forgotten call away from a leak. `gitx` is the lowest package that builds those errors and `kb` already imports it, so the redactor sits where the errors are born. The cost: a hint that prints a redacted URL is not copy-pasteable for a remote that needs inline credentials (those come from the `<kb>.token` file or a helper).

**Alternatives rejected.** A new leaf package for the redactor: one more package for one function with no second user. Redacting only at the call sites in `cmd/`: the `gitx` errors are also returned to `mcpserver` and logged. Keeping the raw value in stored rows and redacting at print time: every printer, including JSON output, has to remember, so the row is stored redacted and comparisons (`sameRemote`) redact both sides.

**Consequences.** A new message that names a remote calls `gitx.RedactRemoteURL`; a new git invocation that echoes output into an error passes it through `RedactRemoteText`. `kb.RedactRemoteURL` no longer exists.
