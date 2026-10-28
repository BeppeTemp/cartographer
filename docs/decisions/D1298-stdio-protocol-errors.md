---
topic: control-plane
---

# D1298 — stdio answers a bad line with a JSON-RPC error and keeps the session, through the classifier HTTP uses

**Decision.** Over stdio a line that is not valid JSON is answered `-32700` (id `null`); an empty batch, a `jsonrpc` other than `2.0` or a non-scalar id is answered `-32600` (carrying the id when it is a string or a number); the loop then reads the next line. Only an I/O error on stdin or stdout ends the session, plus one frame: a line over 16 MiB. HTTP and stdio classify through one function, `protocolErrorCode`. A message is one line.

**Why.** The SDK's stdio reader decodes a JSON stream and stops at the first value it cannot take, so one bad line ended the process through `log.Fatalf` while HTTP, since #1279, answered the same input with an error (#1298). The SDK's connection type cannot be wrapped (its session hook is unexported), so the check is a line filter in front of it, and a line is the only unit that can be skipped after an error. One classifier keeps the two transports from drifting.

**Alternatives rejected.**
- Wrapping the SDK connection to retry after a read error: the interface has an unexported method, so a wrapper cannot satisfy it.
- Keeping the JSON-stream framing: after a syntax error the decoder has no safe place to resume.
- Skipping an over-long line: it would have to be buffered or discarded byte by byte; the limit is the SDK's own and far above the HTTP body limit.

**Consequences.** A message spread over several lines is no longer accepted (the specification already requires one per line). A line over 16 MiB still ends the session. `docs/transport-auth.md` states both.
