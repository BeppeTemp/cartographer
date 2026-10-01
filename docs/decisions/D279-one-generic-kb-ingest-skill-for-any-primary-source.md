---
topic: skills-services-secrets
---

# D279 — One generic `kb-ingest` skill ingests any primary source, patching over creating

**Decision.** A bundled skill, `kb-ingest`, is the procedure for absorbing **one** primary source
into an existing KB. It is source-agnostic: register the source (D278), distil it, find the page
that owns each fact, patch it (create only when no page owns the subject), cite the source in
`provenance`, record unknowns as gaps (D273), verify with `gate_check`, mark the source ingested.
Kind-specific advice lives in one reference file, not in separate skills. The agent reads the
source; the server never does (D28 as amended by D278). A change plan touching more than 5 pages
or creating more than 2 is shown to the operator before any write; smaller ones proceed.

**Why.** Each agent otherwise improvises ingestion and fails the same ways: the same source twice,
new pages where a patch belonged, silent overwrites, dropped unknowns, no trace of origin. The
server pieces (reason D272, gaps D273, history D274, ledger D278) exist; the skill is what makes
them one habit. The cost is a checkpoint that slows large ingestions on purpose.

**Alternatives rejected.** Extending `kb-import`: it maps the structure of a whole foreign corpus
mechanically, the opposite shape of distilling one source's content. One skill per source kind:
the procedure is identical, only the extraction hints differ. A server-side ingest tool: D28 and
D278 keep fetching and parsing on the client. Overwriting a conflicting claim: a contradiction
must stay visible, so the skill opens a `Contradiction` and leaves the page untouched.

**Consequences.** The 5-page and 2-page thresholds are written in the skill so every client
applies the same ones; changing them is a skill version bump. A source's contradictions block the
gate until an operator resolves them, by design. A new bundled skill is provisioned on the next
`cartographer sync`. Credentials found in a source never reach a page: `secret_set` or flagged.
