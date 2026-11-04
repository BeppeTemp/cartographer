---
topic: control-plane
---

# D1635 — Tool errors show paths relative to the KB

**Decision.** A tool result that quotes a Go filesystem error prints the path
below the KB root (or the last element of a path outside it), never the
absolute directory the server keeps its data in. `relPathErr` does it for the
handler errors, and the two-KB transfer scrubs both roots from its error results.

**Why.** A client addresses concepts by id; the host layout is not part of the
contract and differs per deployment. Printing it taught a client of a shared
server something it has no use for, and made the text differ between hosts.

**Alternatives rejected.** A global rewrite in the dispatcher: it does not know
each call's KB roots and would also rewrite text a caller supplied. A plain
`strings.ReplaceAll(err.Error(), root, "")` at each site: it leaves paths
outside the root and the symlink-resolved form of the root.

**Consequences.** A new handler that prints an error that can hold a path uses
`relPathErr(err, k.Root)`; the rule is in `internal/mcpserver/AGENTS.md`.
