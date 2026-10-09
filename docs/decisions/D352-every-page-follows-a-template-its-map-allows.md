---
topic: data-plane
---

# D352 — Every page follows a template its map allows

**Decision.** A page names the template it follows in the frontmatter field
`shape`; a map declares the shapes it accepts (`templates`, `default_template`,
`require_template`); and a template is a closed page schema, declared with flat
`x-template.*` keys in its own frontmatter (required and optional fields, allowed
values, optional sections, section aliases, `open_sections`). Under
`require_template: true` ten checks hold a page to it (`template_missing`,
`template_unknown`, `template_not_allowed`, `template_type_mismatch`,
`template_field_missing`, `template_field_value`, `template_extra_section`,
`template_section_alias`, `template_section_order`, and `template_section_missing`
at warning). Their repairs are mechanical (`set_value shape`, `rename_heading`,
`reorder_sections`) and never write section text. `kb_review` proposes the
template a map's pages already follow (`template_proposal`), and `artifact_write`
may write `templates/` alone by default (`allow_template_write`).

**Why.** A KB could hold two templates of one type, but a page could not say which
it followed, and a page that invented its own structure passed every check: lint
resolved a template by type only, every H2 was required, extras were invisible.
Level 2 of four was chosen (each map declares its shapes, templates are a KB-wide
library, every page binds to one): it keeps reuse across maps and variety inside
one, while excluding invented structure. The cost is a template file to maintain
and a `shape` on every page of a strict map; the unattended doctor pays it, which
is why the induction and the template-only write right exist.

**Alternatives rejected.**
- Opt-in warnings with an allowlist (the first version of this plan): lets invented
  structure through, which is what the KB must exclude.
- One template per map, stored in the map: forbids a host, a service and a runbook
  in one infrastructure map, and forbids reuse of a decision or incident shape.
- A frontmatter field named `template`, `template_id` or `from_template`: the first
  is a write-tool parameter (D289, `tool_param_field`), the others read as tool
  arguments. `shape` is pinned out of `lint.ToolParamFields` by a test.
- Nested `x-template:` block: the frontmatter parser keeps a nested block opaque;
  map contracts already use flat dotted keys.
- `type` as the primary binding with `shape` following: `shape` is the specific
  choice and `type` its OKF projection, so a mismatch fixes the type.
- A fix that adds the missing section: an empty heading is a hollow promise; the
  doctor fills it from sources or records a gap.
- Opening `artifact_write` fully by default: templates are KB-only and never
  provisioned (D109), the other artifacts execute on client machines.

**Consequences.** Every check is warning or info (D289), and a map that sets none
of `templates`, `default_template`, `require_template`, `template_sections` reads
no template and reports nothing new; only `map_without_templates` (info) is
emitted on maps of three pages or more without `templates`. `template_section_order`
is repaired before `template_section_alias` in the default `auto_repair` list: the
reorder lists the headings as written. A reorder normalises the blank lines between
H2 blocks and never moves a heading inside fenced code. `concept_new` strips every
`x-template.*` key, stamps `shape` and never refuses a template outside the map's
list: the write response carries `template_not_allowed`. Templates stay outside
`WalkConcepts`, search and the graph (D109).
