---
topic: transport-auth
---

# D2210 — A start closes the attempts a crash left open with an `interrupted` completion

**Decision.** `audit.Open` with the writer lock appends, for each v2 attempt of the active segment
with no completion, a signed completion with the new outcome `interrupted` (request, tool, KB,
transport and principal copied from the attempt). `audit verify` reports an unclosed attempt as a
distinct `IncompleteError` (records, times, "the server stopped during a write") with exit code 3,
not 1; a closed one counts as `interrupted` and verifies clean.

**Rationale.** D119 made a dead process visible as an unmatched attempt, but nothing could
acknowledge it, so verify failed forever after one `kill -9` (#2193). Appending keeps D18's chain
append-only and tamper-evident: no record is edited or removed, the gap is closed by a signed record
that states what happened and when. Only the writer-lock holder does it, as no other process can have
a call in flight on the log. It appends without rotating, so a start never seals a segment or re-signs
the index before anything has verified them.

**Consequences.** `interrupted` joins the outcome taxonomy; older verifiers reject it as malformed.
An attempt in an already rotated segment (a call straddling a rotation, then a crash) is not closed
and keeps `verify` at exit 3. A removed tail is still undetectable (D119's documented limit).
