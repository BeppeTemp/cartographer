---
topic: data-plane
---

# D15 — Secret scrubbing: blocking regex, 6 patterns ✅ Removed

The `internal/scrub` package (6 regexes: `private_key`, `aws_key`, `github_token`, `generic_secret`, `bearer_token`, `connection_string`) was removed together with `source_ingest` and the event-driven exporter (June 2026). PII prevention is now the responsibility of the LLM-as-judge quality-gate at the ingest checkpoint.
