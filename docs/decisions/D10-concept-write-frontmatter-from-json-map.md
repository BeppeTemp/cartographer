---
topic: control-plane
---

# D10 — `concept_write`: frontmatter from JSON map

The tool receives the frontmatter as a JSON `map[string]interface{}`; the key order in the file depends on Go map iteration (random). It does not affect the `ContentHash` (canonical ordering). File readability can be improved by building the `Frontmatter` with a conventional order.
