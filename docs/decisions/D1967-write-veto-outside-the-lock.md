---
topic: control-plane
---

# D1967 — A write is vetoed by a hook before the handler, never under the KB lock

**Decision.** `write_veto` hooks are asked in `callToolChecked`, after the core
authorization and before the tool handler, for a tool that writes and only on the
target KB's server. A deny is the coded denial `denied_by_hook`, a failure under
`fail_closed` is `hook_unavailable`, and the call is audited as an authorization
denial with the resources `hook` and `hook_verdict`. Nothing under `k.WithGitLock`
calls a hook. A write into another KB (`concept_transfer`, `artifact_move`) is judged on both KBs, the source's hooks and the target's.

**Why.** `gitWrap` runs the sync, the handler, the write gate, the commit and the
push inside the per-KB lock, so a hook call there would stall every writer of the
KB for the length of a timeout. Placing the veto in the authorizer would skip it for
an admin (the shortcut returns first) and would run it again in
`reauthorizeUnderLock`. The cost is a limit: the veto judges the request, not the
final diff, and state may change between the veto and the lock.

**Alternatives rejected.** A veto inside `gitWrap`: holds the lock across a network
call. A veto in the authorizer: skipped for an admin, and repeated under the lock.
Re-asking the hook under the lock: same stall. A content-aware veto: the payload
would carry content; that is the write gate (`judgeWrite`), which is the core's.

**Consequences.** A hook is never called for a request the core refused. The
payload is the request's allow-listed identifiers, the principal and the reason, in
hook version 1. A rule in `internal/mcpserver/AGENTS.md` and two tests keep the lock
free of hooks; a routed view leaves the call to the target KB's server so it happens
once.
