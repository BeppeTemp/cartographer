---
topic: client-configurator
---

# D113 — One client status snapshot across CLI and dashboard

**Status: implemented (2026-07-28).**

**Decision.** Client status is collected once into a versioned,
renderer-independent snapshot. Table commands, JSON output and the Bubble Tea
dashboard consume that snapshot. A failed endpoint request produces one
classified server error and marks connected provider state unknown; JSON keeps
the wrapped cause while human output gives the endpoint and an actionable next
step. Command discovery is grouped and conservative, and the dashboard adapts
its presentation and visible actions to terminal width and selection.

**Rationale.** Independent render paths had drifted into separate network calls
and contradictory repeated errors. A small stable schema gives scripts a safe
contract while keeping the terminal interface concise, and makes the dashboard
an alternate view of the same facts rather than a second status implementation.
