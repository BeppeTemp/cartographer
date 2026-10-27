---
topic: control-plane
---

# D1196 — A template proposal can be adopted: open bodies, standard fields, one item per map

**Decision.**
- A proposed template with no section is open (`x-template.open_sections: true`). When an
  item's impact covers more than half of a map's pages, its new templates are re-induced in
  open form (the evidence says why); if the open form still fails more than half, the item is
  not offered.
- Only standard fields are required: `timestamp`, `provenance`, `status` and what a map
  contract of the item names (required fields, `field_values`, `open_field`). A frequent
  non-standard field is declared nowhere and listed in the evidence as a hint.
- One item per connected component of the map-slug graph, carrying `templates`
  (`{slug, type, template, pages}`); `template_slug`/`template` stay only for a one-template
  item. Every map is in exactly one item and every slug in exactly one.
- The impact says which page types gain `template_missing` (`unbound_types`) and why:
  `default_template` binds only pages of its own type.

**Why.** Proposals that failed every page, made legacy fields mandatory, repeated one
`map_update` in two items, or left `template_missing` unexplained were never applied, so they
stayed in the review list for ever (#1196).

**Alternatives rejected.**
- One item per map only: a slug spanning two maps would be written twice, against D720's one
  slug = one template file.
- One item per slug (D720): a map with two types got two items with the same `map_update`.
- Declaring non-standard frequent fields as optional: still legitimises synonyms the KB avoids.
- Dropping a failing proposal outright: the open form is a structure the pages do follow.

**Consequences.** Consumers read `templates`, not only `template_slug`. A map whose pages mostly
have no template of their own gets no proposal until the other types are written. Weight is the
component's page count.
