# References

## Project foundations

- **Karpathy, A. — "LLM Wiki"** (gist, Apr 2026). raw→wiki→schema pattern; ingest/query/lint; `index.md`/`log.md`; knowledge that composes.
  https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f

- **Google Cloud — "Open Knowledge Format (OKF) v0.1"** (Jun 2026).
  https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md

## MCP protocol

- **MCP — Transports & Authorization** (spec 2025-03-26 / 2025-11-25 / draft; RC 2026-07-28).
  https://modelcontextprotocol.io/specification/draft/basic/transports

## Secrets and skills

- **Mozilla/CNCF SOPS** (v3.13.x).
  https://github.com/getsops/sops

- **age** (X25519, modern encryption).
  https://pkg.go.dev/github.com/getsops/sops/v3/decrypt

- **Agent Skills standard** (`SKILL.md`, agentskills.io, Dec 2025).
  https://agentskills.io/specification

## Providers

- Claude Code: https://code.claude.com/docs
- OpenAI Codex CLI: https://developers.openai.com/codex
- Kiro: https://kiro.dev/docs
- OpenCode: https://opencode.ai/docs

## State of the art (June 2026)

- **Contradictions**: Wikidata rank+references; RDF 1.2; Graphiti/Zep; argumentation frameworks.
- **Human-on-the-loop ingest**: EU AI Act Art.14; PR-based pattern.
- **Real-world cases**: Datadog (AI content marked, scrubbing, per-section model), Zalando (Claude on 2 years of postmortems, curation from 100% to sampling), Stripe (human review on every PR).
