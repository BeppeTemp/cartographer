---
topic: data-plane
---

# D715 — concept_new template variables accept lists, only as whole values

**Decision.** `concept_new` `vars` values are a string or a list of strings. A list fills a frontmatter value that is exactly `{{name}}` (the key becomes a list) or is spliced in place of a list item that is exactly `{{name}}`. A list used anywhere else (inside a larger string, in a list item with other text, in the body) is an error and nothing is written. The caller's value decides the kind; templates declare nothing.

**Why.** With string-only vars, `roles: [{{roles}}]` plus `"a, b"` yields one quoted item, so a multi-item list could not come from a variable. Whole-value positions are the only ones where a list has a single unambiguous YAML rendering.

**Alternatives rejected.** Joining a list into text with a separator: invents a separator and hides data loss. Auto-splitting strings on commas: changes today's output for string-only calls. A per-variable kind in `template_list`: extra surface for a choice the caller already makes.

**Consequences.** String-only calls render byte-identically. List items are inserted literally like strings. Any new template position that renders a variable must go through the same whole-value check.
