---
topic: client-configurator
---

> **Superseded by [D1857](D1857-tui-removed.md)**: the dashboard is removed. The `runSync` rule itself stays true for `cartographer sync`.

# D1612 — The dashboard's sync is `runSync`, with its output redirected

**Decision.** The dashboard's `s`, `S` and `enter` call `runSync` under the client lock, restricted to the chosen providers, exactly as `cartographer sync --client` does. `runSync` reports to `syncOptions.Streams` (zero value: the process streams); the dashboard passes buffers and shows the first line of stderr in its message. `S` is one call for all connected providers. Connecting from the dashboard runs the same KB choice step as the CLI (D190) after a ready probe, and carries the recorded `allow_insecure_http`.

**Why.** The dashboard kept a reduced copy of sync that skipped the known-KB refresh, the MCP entry reconciliation (D288, D737, D1097), derived workspace bindings (D1019), the unsupported-provider handling (D898), the preflight and the partial sync (D257): the two implementations drifted, and shared code printed under the inline frame.

**Alternatives rejected.** Keeping the second path and documenting what it skips: cheaper, but the drift continues. Swapping `os.Stdout` during the run: racy with the renderer and invisible in signatures.

**Consequences.** Anything `runSync` or the helpers it calls report must go through the streams it was given, not `fmt.Print*`; the per-provider progress line of `S` is gone. The dashboard does not run the CLI's config realign or the stale-service replacement, which print and restart a service.
