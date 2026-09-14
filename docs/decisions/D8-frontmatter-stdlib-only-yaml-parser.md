---
topic: data-plane
---

# D8 — Frontmatter: stdlib-only YAML parser

Hand-rolled parser in `internal/okf` (`ParseFrontmatter`/`Serialize`/`CanonicalString`): covers the OKF YAML subset (scalars, flow and block lists, empty values). `*Frontmatter` interface separated from serialization → `gopkg.in/yaml.v3` pluggable without rewrites.
