---
topic: transport-auth
---

# D756 — A routed `?kbs=` serves the mounted subset and reports the unknown names

**Decision.** On `/mcp/routed`, the effective set is `?kbs=` intersected with the mounted KBs. Names that are not mounted are reported, not dropped silently: `kb_status.connection.unknown`, an extended hint, the error of a call naming one (pointing at `cartographer sync`), and one server-side log line per distinct (set, unknown) pair. When no name is mounted the request stays `400`, listing every unknown name sorted and the mounted KBs. Amends D288.

**Why.** A client URL written before a `kb rename` or an unmount made every request fail, so the agent lost the mounted KBs too and had no hint. The mount set changes only across a restart, so a name becomes unknown exactly then. D288 rejected silent dropping because a typo would hide a KB without a trace; reporting through the session channel D712 added keeps that guarantee.

**Alternatives rejected.** Keep the 400 (loses every KB for one stale name). Fall back to all mounted KBs when nothing matches (widens a binding that only narrows). Empty set (a server with no tools). A warning in `initialize` instructions (the server sends none, and an open session has consumed them). Aliasing old to new names (needs a `renamed_from` record and changes `kb rename`, D177): a separate follow-up.

**Consequences.** A stale URL now connects instead of failing with 400. Unknown names are request-scoped (request context), never stored on the cached per-set `Server`, so URLs differing only in stale names share one view. An empty effective set must stay an error (comment in `routedFor`).
