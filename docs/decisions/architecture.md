# Architecture decisions

The original cross-cutting architectural decisions (AD). They are retained as
historical design context; the current product contract lives in
[`../overview.md`](../overview.md) and the topic documentation.

These records explain why choices were made and may describe superseded behavior.
For the supported interface, follow the current-state page linked above.

## AD1–AD13 — Original architecture

| ID | Decision | Details in |
|---|---|---|
| AD1 | Two profiles: **local Core** (v1, single-user, stdio) + **Server** (opt-in add-on, multi-KB, HTTP+OAuth) | Original design; superseded by the unified runtime described in `overview.md` |
| AD2 | General purpose + multi-KB; no assumed domain; separate git repos per KB | `overview.md`, `data-plane.md` |
| AD3 | Authentication via token/OAuth; sync via git; per-KB scopes in the token; `access` is non-enforced metadata until the scope is active | `transport-auth.md`, `loop.md` |
| AD4 | Server CLI-only (administrative only); TUI only in the multi-provider configurator | `interoperability.md`, `deployment.md` |
| AD5 | Canonical transport = Streamable HTTP + OAuth 2.1; stdio for local; `mcp-remote` for OpenCode; stateless-friendly | `transport-auth.md` |
| AD6 | Single-writer on a working branch (never on `main`); Core → fast-forward on light gate; Server → PR with rebase+gate; `if_match` anchored to `main` at PR opening | `concurrency.md` |
| AD7 | Optimistic concurrency: per-KB mutex + git rebase/retry; `if_match` on normalized per-section content-hash; per-dossier lock with lease/TTL | `concurrency.md` |
| AD8 | `needs-resolution` unblocked via `conflict_resolve` (gated tool/CLI, no deadlock); writes rejected with `Retry-After` | `concurrency.md` |
| AD9 | Hybrid contradictions: `Contradiction` node for HARD (gate on `resolution_status: open`), edge for SOFT; bi-temporal supersession with mandatory `reason` | `control-plane.md`, `data-plane.md` |
| AD10 | Ingest via `concept_write` with light gate in Core, PR in Server; OKF validation and absence of PII are blocking | `loop.md`, `data-plane.md` |
| AD11 | Semantic search active from the start in the Server profile (Ollama + in-memory vector store); keyword always available | `control-plane.md` |
| AD12 | User-defined types; `Service` and `Contradiction` have a formal schema verified by `validate()` in `strict` mode | `data-plane.md` |
| AD13 | Skills/services/secrets in the KB: signed+pinned `SKILL.md`, `type:Service`, SOPS with mandatory break-glass | `skills-services-secrets.md` |

---
