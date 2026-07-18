# Operating loop: ingest, query, lint

The loop is the backbone of the Karpathy pattern: every cycle enriches the wiki with new knowledge, answering with citations and maintaining consistency.

## Ingest

1. The agent reads the source, discusses its key points, writes to the KB's **working branch** (never to `main`) via `concept_write`.
2. **Checkpoint calibrated to the profile:**
   - **Local Core**: lightweight gate (validate + lint + commit_gate) → fast-forward to `main`.
   - **Server**: PR on the dedicated branch → merge with rebase + gate.
3. **Safe-outputs** before the checkpoint: OKF/frontmatter/link validation, absence of PII. A **quality-gate LLM-as-judge** (faithfulness/grounding, statement-level) runs beforehand and modulates the intensity: high grounding + small diff → lightweight gate; low grounding or new content → strong gate. For robustness against injection, the judge uses a **different provider** and does not treat the content as instructions.
4. **Opt-in batching** only for trusted sources and low-risk diffs. `commit_gate` and hard contradictions are **always active**.

## Query

`search` + `index_get` → `concept_read(id, section)` (bounded) → synthesis **with citations**.

**Temporal query** via `valid_from`/`valid_to`: "what's true now" / "as-of date X".

**Compounding**: if the answer is valuable, it gets trimmed as a new concept.

## Lint

| Mode | When | Cost |
|---|---|---|
| **Scoped** | After every ingest; only changed nodes + graph neighbors | Cheap, frequent |
| **Full sweep** | Rare | Deterministic part in shell/grep (orphans, stubs, broken links, stale claims via decaying `confidence`); reasoning checks use the model |
| **Deep / cross-model** | Periodic, on large KBs | Semantic comparison between tag-overlapping pairs, with a **different provider** from the one that did the ingest (mitigates the self-referential loop). Token budget cap (100 pages ≈ 300k+ tokens). Emits soft edges or, if hard, a `Contradiction` node. |

## Loop capabilities and costs

- **Human checkpoint**: define sustainable PRs/day per reviewer with **backpressure** (growing queue → ingest slows down) to avoid rubber-stamping.
- **Deep lint**: replace O(n²) pairwise comparison with **pre-filtering via clustering/ANN** on the embeddings (~O(n log n)); token budget/month per KB; unevaluated pairs in a persistent queue (debt tracked with a coverage metric).
- **Single-provider fallback**: if only one provider is available, cross-model lint is **disabled with a warning** (running it on the same model would defeat the mitigation).
- **Cost observability**: tokens spent (ingest, judge, deep lint, embedding) tracked in the audit log and aggregated as a metric. See [`deployment.md`](deployment.md) §observability.
