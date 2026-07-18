---
type: AgentContract
title: Agent Contract
---
# Agent Contract

Contract for agents operating on this KB.

## Mandatory rules (enforced by the server)

- Every concept's frontmatter MUST contain the `type` field.
- Concept IDs must NOT have the `data/` prefix and must NOT have a `.md` extension.
- To write a concept: `concept_write` with `id`, `frontmatter` (map with mandatory `type`), `body`.

## Style

- Descriptive titles, concise and dense body.
- One concept = one idea. If it covers two ideas, split it.
- Use `search` + `concept_read` for navigation before writing.

## Available archives

- `infra`: Homelab infrastructure (services, network, runbooks).
