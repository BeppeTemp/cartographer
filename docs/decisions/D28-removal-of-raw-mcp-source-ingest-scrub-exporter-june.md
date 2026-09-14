---
topic: data-plane
---

# D28 — Removal of `raw/`, `mcp/`, `source_ingest`, `scrub`, `exporter` (June 2026)

**Decision.** The `raw/` and `mcp/` directories are removed from the KB structure. The `internal/scrub` and `internal/exporter` packages are deleted. The `source_ingest` MCP tool and the `--exporter` flag in `main.go` are removed. The configurator abandons `mcp/wiki.yaml` in favor of CLI flags (`--name`, `--transport`, `--url`, `--auth`, `--token-env`).
**Rationale.** The complexity of `source_ingest` (copy into `raw/`, secret scrubbing, webhook exporter, content-hash provenance) did not hold up cost/benefit-wise for the small-to-medium KBs that are the primary target. The direct write cycle via `concept_write` is simpler, verifiable, and composable with existing LLM tools. The PII quality guarantee moves to the LLM-as-judge quality-gate at the ingest checkpoint. `mcp/wiki.yaml` added a file that every user had to keep aligned with their CLI flags — the defaults in `DefaultConfig()` cover the common case without extra files.
