---
topic: concurrency-git
---

# D103 — Observable replication failures and native Git identity fallback

**Decision.** Git replication state is exposed through the read-only `sync_status` tool. Failed commits and pushes remain non-fatal to local writes but are retained until a real push succeeds; debounced writes report pending rather than pretending their future push succeeded. When Cartographer has no complete configured author pair, commits use Git's own resolved identity; the placeholder is retained only as Git's final fallback.

**Rationale.** Local durability and remote replication are separate states. Reporting their distinction preserves offline operation while making enterprise forge push-rule failures actionable without log scraping.
