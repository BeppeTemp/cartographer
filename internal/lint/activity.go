package lint

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// Check activity (D371). Every check runs by default; the few that cannot are
// the ones with nothing to compare against (no templates, no glossary, no
// sources…) or that every map able to run them opted out of. Inactive says
// which and why, so a reader of the Health coverage can tell "checked and
// clean" from "cannot run here".

// templateStrictChecks run on a strict map only (D352): the template_* checks
// other than the section ones.
var templateStrictChecks = []string{
	"template_missing", "template_unknown", "template_not_allowed", "template_type_mismatch",
	"template_field_missing", "template_field_value", "template_extra_section",
	"template_section_alias", "template_section_order",
}

// Inactive returns, by check name, the reason a check cannot run on this KB. A
// check absent from the result is active. The reason is a short sentence a
// reader can act on.
func Inactive(k *kb.KB) map[string]string {
	out := map[string]string{}
	archives, _ := k.ListArchives()
	contracts := map[string]kb.MapContract{}
	for _, a := range archives {
		if c, err := k.ReadMapContract(a); err == nil {
			contracts[a] = c
		}
	}
	any := func(pick func(kb.MapContract) bool) bool {
		for _, c := range contracts {
			if pick(c) {
				return true
			}
		}
		return false
	}
	set := func(reason string, checks ...string) {
		for _, c := range checks {
			out[c] = reason
		}
	}

	// Templates (D352): per map, strict by default once it declares templates.
	declares := any(func(c kb.MapContract) bool { return c.HasTemplateKeys() })
	strict := any(func(c kb.MapContract) bool { return c.StrictTemplates() })
	switch {
	case strict:
	case declares:
		set("every map that declares templates sets require_template: false", templateStrictChecks...)
	default:
		set("no map declares templates (map_without_templates lists the maps that could)", templateStrictChecks...)
	}
	if !strict && !any(func(c kb.MapContract) bool { return c.TemplateSections }) {
		if declares {
			set("every map that declares templates sets require_template: false", "template_section_missing")
		} else {
			set("no map declares templates (map_without_templates lists the maps that could)", "template_section_missing")
		}
	}

	if len(newDriftData(k, nil, contracts).palette) == 0 {
		set("no type palette: no template declares a type and no map is strict", "unknown_type")
	}
	if !any(func(c kb.MapContract) bool { d, _ := EffectiveStaleAfter(&c); return d > 0 }) {
		set("no map ages its open pages (a journal, or a map with open_statuses or stale_after)", "stale_open")
	}
	if !any(func(c kb.MapContract) bool { return len(c.RequiredFields) > 0 || len(c.RequiredFieldsByType) > 0 }) {
		set("no map declares required_fields", "missing_required_field")
	}
	if !any(func(c kb.MapContract) bool { return len(c.FieldValues) > 0 || len(c.FieldValuesByType) > 0 }) {
		set("no map declares field_values", "invalid_field_value")
	}
	if !any(func(c kb.MapContract) bool { return len(c.ForbiddenFields) > 0 }) {
		set("no map declares forbidden_fields", "forbidden_field")
	}
	if !any(func(c kb.MapContract) bool { return c.RequireIndexEntry && c.Index != kb.IndexGenerated }) {
		set("no map keeps a curated index it requires complete (require_index_entry; index: generated maps need no entries)", "index_incomplete")
	}
	if !any(func(c kb.MapContract) bool { return c.Index == kb.IndexGenerated }) {
		set("no map has a generated index (index: generated)", "index_stale")
	}

	if g, err := k.ReadGlossary(); err != nil || !hasForbiddenTerm(g.Glossary) {
		set("glossary.yaml declares no forbidden term", "forbidden_term")
	}
	if st, err := k.ReadPathRegistry(); err != nil || !st.Present {
		set("no paths.yaml: missing_registry nudges when placeholders are cited", "unknown_placeholder", "unused_placeholder")
	}
	if fi, err := os.Stat(filepath.Join(k.Root, "secrets")); err != nil || !fi.IsDir() {
		set("the KB has no secrets/ directory", "sops_missing_file")
	}
	if len(readInstructions(k).legacyPaths) == 0 {
		set("instructions.md declares no legacy_paths", "legacy_path")
	}
	if !hasSourceConcept(k) {
		set("no Source concept registered", "source_uncited")
	}
	return out
}

func hasForbiddenTerm(g kb.Glossary) bool {
	for _, t := range g.Terms {
		if len(t.Forbidden) > 0 {
			return true
		}
	}
	return false
}

// hasSourceConcept reports whether the KB registers at least one Source.
func hasSourceConcept(k *kb.KB) bool {
	found := false
	_ = k.WalkConceptPaths(func(_ okf.ConceptID, _ string, content string) error {
		if found || !strings.Contains(content, kb.SourceType) {
			return nil
		}
		raw, _, ok := okf.SplitFrontmatter(content)
		if !ok {
			return nil
		}
		if fm, err := okf.ParseFrontmatter(raw); err == nil && fm.Type() == kb.SourceType {
			found = true
		}
		return nil
	})
	return found
}
