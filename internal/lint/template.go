package lint

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// Template checks (D352). Every page of a map that sets require_template binds
// to one template ("shape") its map accepts, and a template is a closed page
// schema: required fields and values, required and optional H2 sections, their
// order, and the alternative names a heading may carry. All findings are
// warning or info (D289): adopting templates never turns a gate red.
//
// No template is read, and nothing is reported, for a map that sets none of
// templates, default_template, require_template and template_sections.

// Fix kinds of the template checks.
const (
	// FixRenameHeading: Field = the H2 as written (an alias), To = the
	// template's canonical name (template_section_alias).
	FixRenameHeading = "rename_heading"
	// FixReorderSections: To = every H2 of the page as written, one per line,
	// in the order the template asks for (template_section_order). Blocks move
	// whole, extra sections follow the last template section, text before the
	// first H2 stays first; no text is written.
	FixReorderSections = "reorder_sections"
)

// h2Line is one level-2 heading outside fenced code.
type h2Line struct {
	Line int    // index into strings.Split(body, "\n")
	Text string // as written, trimmed (the form kb.H2Headings returns)
}

// h2Lines lists a body's H2 headings with their line index, ignoring fenced
// code and inline spans: the one scanner the section checks and the two
// heading repairs share, so a heading inside a code block is never a section
// and never moved.
func h2Lines(body string) []h2Line {
	var out []h2Line
	for i, line := range strings.Split(kb.MaskCodeSpans(body), "\n") {
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		if h := strings.TrimSpace(strings.TrimRight(strings.TrimPrefix(line, "## "), "#")); h != "" {
			out = append(out, h2Line{Line: i, Text: h})
		}
	}
	return out
}

// RenameHeading rewrites the first H2 whose text is from to to, keeping
// everything else of the body (template_section_alias). It reports false when
// no such heading exists (already renamed: idempotent).
func RenameHeading(body, from, to string) (string, bool) {
	lines := strings.Split(body, "\n")
	for _, h := range h2Lines(body) {
		if h.Text == from {
			lines[h.Line] = "## " + to
			return strings.Join(lines, "\n"), true
		}
	}
	return body, false
}

// ReorderSections rearranges the H2 blocks of body into the order desired
// lists (the page's own headings as written). Text before the first H2 stays
// first. It reports false, and returns body unchanged, when the headings
// present are not exactly the ones listed (the page changed since the finding)
// or when the order is already right. Blocks are separated by one blank line
// afterwards.
func ReorderSections(body string, desired []string) (string, bool) {
	hs := h2Lines(body)
	if len(hs) == 0 || len(hs) != len(desired) {
		return body, false
	}
	lines := strings.Split(body, "\n")
	preamble := trimTrailingBlank(lines[:hs[0].Line])
	blocks := make([][]string, len(hs))
	texts := make([]string, len(hs))
	for i, h := range hs {
		end := len(lines)
		if i+1 < len(hs) {
			end = hs[i+1].Line
		}
		blocks[i] = trimTrailingBlank(lines[h.Line:end])
		texts[i] = h.Text
	}
	used := make([]bool, len(blocks))
	var parts []string
	if len(preamble) > 0 {
		parts = append(parts, strings.Join(preamble, "\n"))
	}
	for _, want := range desired {
		found := false
		for i, t := range texts {
			if !used[i] && t == want {
				used[i], found = true, true
				parts = append(parts, strings.Join(blocks[i], "\n"))
				break
			}
		}
		if !found {
			return body, false
		}
	}
	out := strings.Join(parts, "\n\n")
	if strings.HasSuffix(body, "\n") {
		out += "\n"
	}
	return out, out != body
}

func trimTrailingBlank(lines []string) []string {
	n := len(lines)
	for n > 0 && strings.TrimSpace(lines[n-1]) == "" {
		n--
	}
	return lines[:n]
}

// templateInput is the part of conceptInput the template checks read.
type templateCtx struct {
	in       conceptInput
	strict   bool
	closed   bool
	shape    string // the page's shape as written ("" when absent or not a string)
	hasShape bool
	pageType string
	tpl      kb.TemplateInfo
	bound    bool // tpl is the resolved template
}

// templateFindings computes every template check of one page.
func templateFindings(in conceptInput) []Finding {
	c := in.Contract
	if in.Parsed == nil || c == nil || !c.HasTemplateKeys() {
		return nil
	}
	x := templateCtx{in: in, strict: c.RequireTemplate, pageType: strings.TrimSpace(in.Parsed.Type())}
	if v, ok := in.Parsed.Get(kb.ShapeField); ok {
		x.hasShape = true
		x.shape, _ = v.(string)
		x.shape = strings.TrimSpace(x.shape)
	}
	x.closed = closedPhase(in.Parsed, c)
	x.tpl, x.bound = in.Catalog.Resolve(x.shape, x.pageType, c)

	var out []Finding
	if x.strict {
		out = append(out, x.bindingFindings()...)
	}
	if x.bound && (x.strict || c.TemplateSections) {
		if !x.closed {
			out = append(out, x.missingSections()...)
		}
		if x.strict {
			out = append(out, x.fieldFindings()...)
			out = append(out, x.sectionShapeFindings()...)
		}
	}
	return out
}

func (x templateCtx) finding(check, msg string, fix *Fix) Finding {
	return newFinding(check, Finding{Path: x.in.RelPath, Message: msg, Fix: fix})
}

// directlyBound reports whether the page's shape names an existing template.
func (x templateCtx) directlyBound() (kb.TemplateInfo, bool) {
	if !x.hasShape || !kb.ValidTemplateSlug(x.shape) {
		return kb.TemplateInfo{}, false
	}
	t, ok := x.in.Catalog[x.shape]
	return t, ok
}

// bindingFindings: template_missing, template_unknown, template_not_allowed,
// template_type_mismatch.
func (x templateCtx) bindingFindings() []Finding {
	c := x.in.Contract
	var out []Finding
	switch {
	case !x.hasShape && !x.bound:
		msg := fmt.Sprintf("no %q field and no template resolves for it: map %q requires every page to follow one of its templates", kb.ShapeField, x.in.MapName)
		var fix *Fix
		if cands := x.candidates(); len(cands) == 1 {
			fix = &Fix{Kind: FixSetValue, Field: kb.ShapeField, To: cands[0]}
			msg += fmt.Sprintf(" — the only template of type %q the map accepts is %q", x.pageType, cands[0])
		} else if len(c.Templates) > 0 {
			msg += "; accepted: " + strings.Join(c.Templates, ", ")
		}
		out = append(out, x.finding("template_missing", msg, fix))
	case x.hasShape:
		if _, ok := x.directlyBound(); !ok {
			out = append(out, x.finding("template_unknown",
				fmt.Sprintf("%q is %q, which names no template in templates/ (a slug of lowercase words and hyphens)", kb.ShapeField, x.shape), nil))
		}
	}
	if x.bound && len(c.Templates) > 0 && !containsString(c.Templates, x.tpl.Slug) {
		out = append(out, x.finding("template_not_allowed",
			fmt.Sprintf("template %q is not one of the shapes map %q accepts (%s): move the page to a map that accepts it, or add the template to the map", x.tpl.Slug, x.in.MapName, strings.Join(c.Templates, ", ")), nil))
	}
	if t, ok := x.directlyBound(); ok && x.pageType != "" && t.Type != "" && !strings.EqualFold(t.Type, x.pageType) {
		out = append(out, x.finding("template_type_mismatch",
			fmt.Sprintf("type %q but template %q is of type %q: the shape is the specific choice, the type follows it", x.pageType, t.Slug, t.Type),
			&Fix{Kind: FixSetValue, Field: "type", To: t.Type}))
	}
	return out
}

// candidates are the templates the map accepts that fit a page with no shape:
// those of its type, or the map's default when the page has no type yet
// (missing_type then settles the type, template_type_mismatch never fights it).
func (x templateCtx) candidates() []string {
	c := x.in.Contract
	if x.pageType == "" {
		if c.DefaultTemplate != "" {
			if _, ok := x.in.Catalog[c.DefaultTemplate]; ok {
				return []string{c.DefaultTemplate}
			}
		}
		return nil
	}
	var out []string
	for _, slug := range c.Templates {
		if t, ok := x.in.Catalog[slug]; ok && strings.EqualFold(t.Type, x.pageType) {
			out = append(out, slug)
		}
	}
	return out
}

// sectionNames folds the template's sections and aliases for matching.
type sectionNames struct {
	canon map[string]int // folded canonical name → index in Sections
	alias map[string]int // folded alias → index in Sections
}

func namesOf(t kb.TemplateInfo) sectionNames {
	n := sectionNames{canon: map[string]int{}, alias: map[string]int{}}
	for i, s := range t.Sections {
		if _, dup := n.canon[foldHeading(s)]; !dup {
			n.canon[foldHeading(s)] = i
		}
	}
	for name, alts := range t.Aliases {
		i, ok := n.canon[foldHeading(name)]
		if !ok {
			continue
		}
		for _, a := range alts {
			if f := foldHeading(a); f != "" {
				if _, isCanon := n.canon[f]; !isCanon {
					n.alias[f] = i
				}
			}
		}
	}
	return n
}

// missingSections: template_section_missing. Info under template_sections,
// warning under require_template. An alias counts as the section; no fix, an
// empty heading is a hollow promise.
func (x templateCtx) missingSections() []Finding {
	names := namesOf(x.tpl)
	have := map[int]bool{}
	for _, h := range h2Lines(x.in.Body) {
		f := foldHeading(h.Text)
		if i, ok := names.canon[f]; ok {
			have[i] = true
		} else if i, ok := names.alias[f]; ok {
			have[i] = true
		}
	}
	optional := map[string]bool{}
	for _, s := range x.tpl.OptionalSections {
		optional[foldHeading(s)] = true
	}
	var missing []string
	for i, s := range x.tpl.Sections {
		if !have[i] && !optional[foldHeading(s)] {
			missing = append(missing, s)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	f := x.finding("template_section_missing",
		fmt.Sprintf("missing %d section(s) of its %q template: %s", len(missing), x.tpl.Slug, strings.Join(missing, ", ")), nil)
	if x.strict {
		f.Severity = SevWarning
	}
	return []Finding{f}
}

// fieldFindings: template_field_missing, template_field_value.
func (x templateCtx) fieldFindings() []Finding {
	if x.closed {
		return nil
	}
	var out []Finding
	var missing []string
	for _, field := range x.tpl.RequiredFields {
		v, ok := x.in.Parsed.Get(field)
		if !ok || emptyFrontmatterValue(v) {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		out = append(out, x.finding("template_field_missing",
			fmt.Sprintf("template %q requires %s", x.tpl.Slug, strings.Join(quoteAll(missing), ", ")), nil))
	}
	fields := make([]string, 0, len(x.tpl.FieldValues))
	for f := range x.tpl.FieldValues {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	for _, field := range fields {
		allowed := x.tpl.FieldValues[field]
		value, exists := x.in.Parsed.Get(field)
		if !exists || len(allowed) == 0 {
			continue
		}
		var got []string
		switch v := value.(type) {
		case string:
			got = []string{v}
		case []string:
			got = v
		default:
			got = []string{fmt.Sprint(v)}
		}
		for _, g := range got {
			g = strings.TrimSpace(g)
			if g == "" || fieldValueAllowed(allowed, g) {
				continue
			}
			var fix *Fix
			msg := fmt.Sprintf("field %q has value %q, allowed by template %q: %s", field, g, x.tpl.Slug, strings.Join(allowed, ", "))
			if _, scalar := value.(string); scalar {
				if to, ok := familiesFor(x.in.Contract).canonicalIn(g, allowed); ok {
					fix = &Fix{Kind: FixSetValue, Field: field, To: to}
					msg += fmt.Sprintf(" — fix: set it to %q", to)
				}
			}
			out = append(out, x.finding("template_field_value", msg, fix))
			break
		}
	}
	return out
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

// sectionShapeFindings: template_extra_section, template_section_alias,
// template_section_order.
func (x templateCtx) sectionShapeFindings() []Finding {
	names := namesOf(x.tpl)
	hs := h2Lines(x.in.Body)
	present := map[string]bool{}
	for _, h := range hs {
		present[foldHeading(h.Text)] = true
	}
	var out []Finding
	var extras []string
	type placed struct {
		text string
		idx  int // template index, -1 for an extra
	}
	seq := make([]placed, 0, len(hs))
	for _, h := range hs {
		f := foldHeading(h.Text)
		if i, ok := names.canon[f]; ok {
			seq = append(seq, placed{h.Text, i})
			continue
		}
		if i, ok := names.alias[f]; ok {
			seq = append(seq, placed{h.Text, i})
			canon := x.tpl.Sections[i]
			var fix *Fix
			msg := fmt.Sprintf("section %q is an alias of %q in template %q", h.Text, canon, x.tpl.Slug)
			if !present[foldHeading(canon)] {
				fix = &Fix{Kind: FixRenameHeading, Field: h.Text, To: canon}
			} else {
				msg += "; the page has both, so merge the text by hand"
			}
			out = append(out, x.finding("template_section_alias", msg, fix))
			continue
		}
		seq = append(seq, placed{h.Text, -1})
		extras = append(extras, h.Text)
	}
	if len(extras) > 0 && !x.tpl.OpenSections && !x.closed {
		out = append(out, x.finding("template_extra_section",
			fmt.Sprintf("section(s) %s are not in template %q: merge the text into one of its sections or move it to another page", strings.Join(quoteAll(extras), ", "), x.tpl.Slug), nil))
	}
	// Order: the template sections present, by first occurrence, must ascend.
	last, outOfOrder := -1, false
	seen := map[int]bool{}
	for _, p := range seq {
		if p.idx < 0 || seen[p.idx] {
			continue
		}
		seen[p.idx] = true
		if p.idx < last {
			outOfOrder = true
		}
		last = p.idx
	}
	if outOfOrder {
		var tpl, ext []string
		sorted := make([]placed, 0, len(seq))
		for _, p := range seq {
			if p.idx >= 0 {
				sorted = append(sorted, p)
			}
		}
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].idx < sorted[j].idx })
		for _, p := range sorted {
			tpl = append(tpl, p.text)
		}
		for _, p := range seq {
			if p.idx < 0 {
				ext = append(ext, p.text)
			}
		}
		out = append(out, x.finding("template_section_order",
			fmt.Sprintf("sections are out of the order of template %q", x.tpl.Slug),
			&Fix{Kind: FixReorderSections, To: strings.Join(append(tpl, ext...), "\n")}))
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// mapTemplateMinConcepts is the page count from which a map with no templates
// is worth a template proposal (map_without_templates, kb_review).
const mapTemplateMinConcepts = 3

// mapTemplateFindings are the checks on a map's descriptor: a map of at least
// mapTemplateMinConcepts pages that declares no templates (map_without_templates)
// and a template whose allowed values leave the map's own (contract_malformed).
func mapTemplateFindings(descriptor, mapName string, contract kb.MapContract, concepts int, catalog kb.TemplateCatalog) []Finding {
	var out []Finding
	if len(contract.Templates) == 0 && concepts >= mapTemplateMinConcepts {
		out = append(out, newFinding("map_without_templates", Finding{
			Path:    descriptor,
			Message: fmt.Sprintf("map %q holds %d pages and declares no templates: kb_review proposes the ones its pages already follow", mapName, concepts),
		}))
	}
	slugs := append([]string(nil), contract.Templates...)
	sort.Strings(slugs)
	for _, slug := range slugs {
		t, ok := catalog[slug]
		if !ok {
			continue
		}
		fields := make([]string, 0, len(t.FieldValues))
		for f := range t.FieldValues {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		for _, field := range fields {
			mapAllowed, constrained := contract.FieldValues[field]
			if !constrained {
				continue
			}
			for _, v := range t.FieldValues[field] {
				if !fieldValueAllowed(mapAllowed, v) {
					out = append(out, newFinding("contract_malformed", Finding{
						Path:    descriptor,
						Message: fmt.Sprintf("template %q allows %q for %q, which map %q does not: its x-template.field_values.%s must be a subset of the map's field_values.%s", slug, v, field, mapName, field, field),
					}))
					break
				}
			}
		}
	}
	return out
}
