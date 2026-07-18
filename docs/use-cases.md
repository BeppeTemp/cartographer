# Real-world use cases

## 1. Incident management — a self-updating runbook (SRE)

During an incident, an `append-finding` tool writes the cause/fix in real time into the `incident` KB's runbook and logs it in `log.md` with a snapshot (commit, model, provider). Once the incident is closed, the agent ingests the postmortem via `concept_write` (with `provenance` pointing to the original source) and updates 10-15 related pages in a single pass via a PR.

The query "what's the recurring pattern in Postgres timeouts?" is synthesized with citations to `provenance`. The weekly (cross-model) lint finds two causes attributed to the same symptom: if HARD, it creates a `Contradiction` node (`factual`, `open`) that `commit_gate` escalates to human review.

## 2. Maintenance and runbooks — bi-temporal supersession (Platform engineering)

A procedure changes: the agent reads the changelog from the source and the supervised ingest proposes the plan at the checkpoint. Once approved, `supersede` closes the old claim (`valid_to` + `superseded_by`, `reason` required, history preserved) and creates the new one. `if_match` prevents blind overwrites.

The query "what's the valid rollback procedure today?" uses `valid_from`/`valid_to` (and "as-of date X"). The lint flags orphans, broken links, and stale claims.

## 3. Research and synthesis — scattered notes → a queryable wiki (R&D)

Hundreds of notes/PDFs are ingested by the agent via `concept_write` (with `provenance` per source); **incremental** ingest processes only new sources. The query "what do the sources say about X and where do they diverge?" is synthesized with citations, and where sources conflict on **perspective**, an **issue page** is created (IBIS, `disputed`, `type: perspective`) with typed edges — *surface, not silence*.

At small scale, full-text/keyword retrieval is enough; semantic search comes into play as it grows.

## 4. Multi-provider — cross-model lint and per-stage model selection (Knowledge quality)

The same KB is served over Streamable HTTP + OAuth 2.1; different providers consume it. A cheap model handles extraction, a large one handles the sensitive stages; the LLM-as-judge quality gate verifies grounding **before** the checkpoint and modulates its intensity accordingly.

Lint runs with a **different provider** than the one used for ingest; when two providers disagree on a fact, a `Contradiction` node is created (`asserted_by` per provider). Config adapters: Claude streamable-http/OAuth, OpenCode `mcp-remote`, Kiro native OAuth.

## 5. Multi-KB — separation by domain (Enterprise knowledge platform)

Separate git KBs (`incident`, `runbook`, `research`), each with an `IngestionContract`, `last_indexed_commit`, a single-writer mutex, and an `access` → roles mapping. Each KB synchronizes **independently**; a real conflict puts only that KB into `needs-resolution`. A cross-domain ingest opens separate PRs per KB.

Reads are Resources, writes are gated Tools; compliance-grade RBAC/audit is a future extension.

---

> **Real-world state-of-the-art references**: Datadog (AI-marked content, scrubbing, per-section model), Zalando (Claude on 2 years of postmortems, curation from 100% to sampling), Stripe (human review on every PR). The Agentic Wiki is **complementary** to enterprise RAG, not a replacement; it shines on bounded domains.
