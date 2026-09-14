---
topic: control-plane
---

# D20 — Embedding: interface + Ollama adapter, wired

*(Superseded by D135: the embedder and semantic search were removed.)*
`internal/embed`: `Embedder` interface with `Embed(text) → Vector`. `OllamaEmbedder` adapter (HTTP POST `/api/embed`). In-memory `Store` with cosine similarity. Activated with `--ollama <url>` (or `CARTOGRAPHER_OLLAMA`): the `search` tool supports `use_semantic=true` for hybrid mode (keyword + vector); `index_rebuild` also rebuilds the vector store.
