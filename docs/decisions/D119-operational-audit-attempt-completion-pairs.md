---
topic: transport-auth
---

# D119 — Operational audit: attempt/completion pairs, checkpointed retention, offline verification

**Decision.** Every `tools/call` dispatched over HTTP or stdio now appends two audit events when
`audit.log` is configured (`internal/mcpserver/audit.go`, `Server.SetAuditLog`): an *attempt* before
the handler runs and a *completion* after it, carrying tool, KB, transport, principal and outcome.
The principal is read from the request context populated by D118, not passed as a separate argument.
`audit.mode` selects the failure semantics: `best_effort` (default) counts a failed append and lets
the call proceed; `required` rejects the call before the tool runs. Segments rotate into
`audit.archive_dir`, and `audit.retention_days` may delete a rotated segment only after it is
covered by a **signed checkpoint index**, so verification still succeeds for segments no longer on
disk. Appends are durable (`fsync`) with rollback on a partial write. `cartographer audit
verify|export` reads the files directly and never contacts a running server.

**Rationale.** A single event per call cannot distinguish "the operation did not happen" from "the
process died while it was happening" — the attempt/completion pair makes an interrupted operation
visible as an unmatched attempt, which is exactly the case a compliance review cares about. Both
modes are needed because they encode opposite priorities: most deployments must not lose
availability to a full disk, while a compliance deployment must never execute an operation it cannot
record, and only the caller knows which one it is. Deleting a segment would normally break the hash
chain, so retention is gated on the checkpoint rather than on age alone: the chain stays verifiable
without keeping every byte forever. Verification is deliberately offline because the moment an audit
trail is most needed is when the server is not running.

**Consequences.** `required` mode couples MCP availability to the audit sink's availability: an
unwritable log takes writes down. That is the intended trade, and it is why `best_effort` remains the
default so no existing deployment changes behaviour on upgrade. The two-event scheme roughly doubles
the log's line count and makes a naive `wc -l` over-report operations by 2×. `export` refuses to emit
a report for an unverifiable chain, so a corrupt log yields no document at all rather than a partial
one that would read as authoritative. The fault-injection seam used by the failure-path tests is
exported (`audit.FailAppendsForTest`) because the MCP layer lives in another package and its entire
contract is about what happens when appends fail; it is test-only and never reached in production
code. `audit.mode` and the rotation keys are YAML-only — the existing `CARTOGRAPHER_AUDIT_LOG`
environment variable still enables the log with default best-effort behaviour.
