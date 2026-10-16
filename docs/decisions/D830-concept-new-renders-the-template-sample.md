---
topic: control-plane
---

# D830 — concept_new renders a template's fenced sample and defaults `{{timestamp}}` to today

**Decision.** When a template's sections come from a fenced markdown sample (`kb.TemplatePageBody`, the same scan `templateH2` reads sections with: no H2 outside fenced code, at least one in the first fenced block), `concept_new` renders that sample's content as the page body; the prose around the fence is guidance for the writer and does not reach the page. Any other template renders its body as written. The template's frontmatter and variable substitution apply as before. `{{timestamp}}` defaults to today's date (`YYYY-MM-DD`, UTC, `lint.Now`) when `vars` omits it; a passed value always wins, and no other variable has a default.

**Why.** The sample is the template's section contract, but a page that carried the whole body had its headings inside a fence, where the section check does not look: a page just created from the template reported `template_section_missing`, plus guidance text in every page. Sharing one scanner between the contract and the renderer means the page always satisfies the contract the check reads. The creation date is what almost every caller passes for `timestamp`, and requiring it was pure friction; `id` and `title` are caller intent, so a default would hide a mistake.

**Alternatives rejected.** Keeping the prose and unwrapping the fence in place: leaves the guidance in every new page. A second, separate fence rule in `concept_new`: the two would drift. Defaults for other variables: ambiguous, and a wrong default is silent. A per-variable default declared in the template: extra template surface for one variable. Documenting the default in the `concept_new` description: the `tools/list` budget (D285) has no room for it, so it lives in `docs/control-plane.md`.

**Consequences.** The variables a template needs are those of its frontmatter and its rendered body: a placeholder only the dropped guidance mentions is neither required nor accepted, and `template_list` does not return it; the whole body is still validated. A template without a qualifying fence renders byte-identically. Changing how `templateH2` picks the sample changes the page `concept_new` writes, by design.
