package lint

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// ReviewTemplateProposal (D352) is a group of pages of one type in one map that
// follow a common structure no template names yet: the item carries the
// template text those pages already follow and the map_update arguments that
// bind the map to it. The server only proposes (D14); the doctor writes it.
const ReviewTemplateProposal = "template_proposal"

// Induction thresholds (D352): a section is required when at least
// proposalRequiredShare of the group's pages carry it and optional from
// proposalOptionalShare; a field is required from proposalFieldShare.
//
// An optional section or field is also a claim about the whole map: it needs
// proposalOptionalShare of the pages (half) and at least proposalMinCarriers of
// them. A heading one page of four carries describes that page, not a structure
// the pages share (#687).
const (
	proposalMinPages      = 3
	proposalMinCarriers   = 2
	proposalRequiredShare = 0.60
	proposalOptionalShare = 0.50
	proposalFieldShare    = 0.90
	proposalMaxValues     = 8
	proposalMaxExamples   = 5
)

// proposalSkipFields never become required or optional fields of a proposed
// template: the template's own frontmatter carries them, or they are free text.
var proposalSkipFields = map[string]bool{
	"type": true, "title": true, kb.ShapeField: true, "lint_ignore": true,
}

// proposalNoValueFields are fields whose values are never a vocabulary.
var proposalNoValueFields = map[string]bool{
	"description": true, "timestamp": true, "review_after": true, "superseded_by": true,
	"waiting_on": true, "provenance": true, "tags": true, "resource": true, "secrets_source": true,
}

// proposalGroup is the pages of one type that would bind to one template slug:
// the qualifying pages of every map that would declare it (#687). A template is
// one file per KB, so the slug, not the map, is the unit of a proposal.
type proposalGroup struct {
	typ   string
	pages []*reviewConcept // each page carries its own mapName
	maps  []string         // sorted
	// following counts the pages that already follow templates/<slug>.md, when it exists.
	following int
}

// mapGroup is the pages of one type in one map, before the maps are merged.
type mapGroup struct {
	mapName string
	typ     string
	pages   []*reviewConcept
}

// perMap returns the group's pages of one map.
func (g *proposalGroup) perMap(name string) []*reviewConcept {
	var out []*reviewConcept
	for _, p := range g.pages {
		if p.mapName == name {
			out = append(out, p)
		}
	}
	return out
}

// templateProposalItems proposes, per template slug, the template the pages of
// one type already follow across the maps that would declare it. A group
// qualifies when its pages bind to no template: with at least proposalMinPages
// pages for a template still to write, or with at least proposalRequiredShare
// of them following the template that already exists, whatever their number. In
// a map that declares templates, most of the pages must also be unbound.
// Each item carries the impact of declaring it. Deterministic: the same KB gives
// byte-identical items.
func templateProposalItems(concepts []*reviewConcept, contracts map[string]kb.MapContract, catalog kb.TemplateCatalog) []ReviewItem {
	groups := map[[2]string]*mapGroup{}
	for _, c := range concepts {
		if c.mapName == "" || c.fm == nil || strings.TrimSpace(c.typ) == "" {
			continue
		}
		key := [2]string{c.mapName, c.typ}
		g := groups[key]
		if g == nil {
			g = &mapGroup{mapName: c.mapName, typ: c.typ}
			groups[key] = g
		}
		g.pages = append(g.pages, c)
	}
	keys := make([][2]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})

	bySlug := map[string]*proposalGroup{}
	var slugOrder []string
	// mapSlugs: per map, the slugs it would declare, with its page count.
	mapSlugs := map[string]map[string]int{}
	for _, key := range keys {
		g := groups[key]
		contract, ok := contracts[g.mapName]
		if !ok {
			continue
		}
		slug := Slugify(g.typ)
		if !kb.ValidTemplateSlug(slug) {
			continue
		}
		// The floor is for induction: a template to write needs enough pages to
		// generalise from. One that already exists is only matched against the pages.
		_, existing := catalog[slug]
		if !existing && len(g.pages) < proposalMinPages {
			continue
		}
		if len(contract.Templates) > 0 {
			unbound := 0
			for _, p := range g.pages {
				shape, _ := frontmatterValue(p.fm, kb.ShapeField).(string)
				if _, bound := catalog.Resolve(strings.TrimSpace(shape), p.typ, &contract); !bound {
					unbound++
				}
			}
			if float64(unbound)/float64(len(g.pages)) <= 0.5 {
				continue
			}
		}
		following := 0
		if existing {
			for _, p := range g.pages {
				if followsTemplate(p, g.mapName, contract, catalog, slug) {
					following++
				}
			}
			if float64(following)/float64(len(g.pages)) < proposalRequiredShare {
				continue
			}
		}
		sg := bySlug[slug]
		if sg == nil {
			sg = &proposalGroup{typ: g.typ}
			bySlug[slug] = sg
			slugOrder = append(slugOrder, slug)
		}
		sg.pages = append(sg.pages, g.pages...)
		sg.following += following
		// Two type spellings in one map share a slug: the map counts once.
		if !slices.Contains(sg.maps, g.mapName) {
			sg.maps = append(sg.maps, g.mapName)
		}
		if mapSlugs[g.mapName] == nil {
			mapSlugs[g.mapName] = map[string]int{}
		}
		mapSlugs[g.mapName][slug] += len(g.pages)
	}

	// mapUpdateFor binds one map to every slug it would declare.
	mapUpdateFor := func(name string) map[string]interface{} {
		slugs := map[string]bool{}
		for _, t := range contracts[name].Templates {
			slugs[t] = true
		}
		biggest, most := "", -1
		for sl, n := range mapSlugs[name] {
			slugs[sl] = true
			if n > most || (n == most && sl < biggest) {
				biggest, most = sl, n
			}
		}
		all := make([]string, 0, len(slugs))
		for sl := range slugs {
			all = append(all, sl)
		}
		sort.Strings(all)
		return map[string]interface{}{"map": name, "templates": all, "default_template": biggest}
	}

	texts := map[string]string{}
	proposed := map[string]kb.TemplateInfo{}
	for _, slug := range slugOrder {
		if _, have := catalog[slug]; have {
			continue
		}
		texts[slug] = proposedTemplate(bySlug[slug], contracts)
		if info, ok := kb.ParseTemplate(slug, texts[slug]); ok {
			proposed[slug] = info
		}
	}
	impacts := map[string]TemplateImpact{}
	for name := range mapSlugs {
		impacts[name] = templateImpact(name, concepts, contracts[name], catalog, proposed, mapUpdateFor(name))
	}

	var out []ReviewItem
	for _, slug := range slugOrder {
		g := bySlug[slug]
		sort.Strings(g.maps)
		concepts := make([]string, len(g.maps))
		for i, m := range g.maps {
			concepts[i] = m + mapDescriptorSuffix
		}
		_, have := catalog[slug]
		where := fmt.Sprintf("map %q", g.maps[0])
		if len(g.maps) > 1 {
			where = fmt.Sprintf("%d maps (%s)", len(g.maps), strings.Join(g.maps, ", "))
		}
		evidence := fmt.Sprintf("%d %s pages in %s follow a common structure and bind to no template", len(g.pages), g.typ, where)
		action := fmt.Sprintf("artifact_write templates/%s.md with the `template` text, then map_update with `map_update` (every entry of `map_updates` when it lists several maps); %s", slug, bindingNote)
		text := ""
		if have {
			evidence = fmt.Sprintf("%d %s pages in %s follow templates/%s.md, which the map does not declare", g.following, g.typ, where, slug)
			action = "map_update with `map_update` (every entry of `map_updates` when it lists several maps) to declare it; " + bindingNote
		} else {
			text = texts[slug]
		}
		var impact []TemplateImpact
		for _, m := range g.maps {
			if im := impacts[m]; im.Pages > 0 {
				impact = append(impact, im)
			}
		}
		publicEvidence := evidence
		if clause := impactClause(impact); clause != "" {
			evidence += "; " + clause
		}
		item := ReviewItem{
			Impact:          impact,
			Kind:            ReviewTemplateProposal,
			Concepts:        concepts,
			Evidence:        evidence,
			publicEvidence:  publicEvidence,
			SuggestedAction: action,
			Weight:          len(g.pages),
			TemplateSlug:    slug,
			Template:        text,
		}
		if len(g.maps) == 1 {
			item.MapUpdate = mapUpdateFor(g.maps[0])
		} else {
			for _, m := range g.maps {
				item.MapUpdates = append(item.MapUpdates, mapUpdateFor(m))
			}
		}
		out = append(out, item)
	}
	return out
}

// bindingNote says how pages bind once a map declares a template: by resolution
// (TemplateCatalog.Resolve), not by a repair.
const bindingNote = "once the map declares it, pages bind through templates/<type>.md or the map's default_template, and kb_repair template_missing only sets `shape` on a page that still resolves to nothing"

// templateChecks are the findings that say a page does not follow its template.
var templateChecks = map[string]bool{
	"template_field_missing": true, "template_field_value": true, "template_section_missing": true,
	"template_section_order": true, "template_section_alias": true, "template_extra_section": true,
	"template_not_allowed": true, "template_type_mismatch": true,
}

// pageTemplateFindings runs the template checks lint runs on one page of a map.
func pageTemplateFindings(p *reviewConcept, mapName string, contract kb.MapContract, catalog kb.TemplateCatalog) []Finding {
	return templateFindings(conceptInput{RelPath: p.path, Body: p.body, Parsed: p.fm, MapName: mapName, Contract: &contract, Catalog: catalog})
}

// followsTemplate reports whether a page passes lint's template checks against
// templates/<slug>.md, were the map to declare it: the same checks as lint, not
// a second implementation of them (D352).
func followsTemplate(p *reviewConcept, mapName string, contract kb.MapContract, catalog kb.TemplateCatalog, slug string) bool {
	c := contract
	c.Templates = append(slices.Clone(contract.Templates), slug)
	c.DefaultTemplate = slug
	for _, f := range pageTemplateFindings(p, mapName, c, catalog) {
		if templateChecks[f.Check] {
			return false
		}
	}
	return true
}

// FollowsTemplate reports whether the concept id, with this content, passes
// lint's template checks against templates/<slug>.md, were the map to declare
// it as its default_template. It is followsTemplate for a caller outside the
// package (source_register, D1072): same checks, no second implementation.
func FollowsTemplate(mapName string, id okf.ConceptID, content string, contract kb.MapContract, catalog kb.TemplateCatalog, slug string) bool {
	fmRaw, body, _ := okf.SplitFrontmatter(content)
	p := &reviewConcept{id: id, mapName: mapName, body: body, path: string(id) + ".md", content: content}
	if parsed, _ := okf.ParseFrontmatter(fmRaw); parsed != nil {
		p.fm = parsed
		p.typ = parsed.Type()
	}
	return followsTemplate(p, mapName, contract, catalog, slug)
}

// templateImpact previews what declaring the proposed templates does to one map:
// the template findings its pages gain, over those they already have. It runs
// lint's own checks on every page of the map, whatever its type, so the preview
// cannot drift from what lint reports once the update is applied.
func templateImpact(mapName string, concepts []*reviewConcept, current kb.MapContract, catalog kb.TemplateCatalog, proposed map[string]kb.TemplateInfo, update map[string]interface{}) TemplateImpact {
	after := current
	after.Templates, _ = update["templates"].([]string)
	after.DefaultTemplate, _ = update["default_template"].(string)
	merged := kb.TemplateCatalog{}
	for k, v := range catalog {
		merged[k] = v
	}
	for _, slug := range after.Templates {
		if info, ok := proposed[slug]; ok {
			merged[slug] = info
		}
	}
	im := TemplateImpact{Map: mapName}
	pages := map[string]bool{}
	for _, p := range concepts {
		if p.mapName != mapName || p.fm == nil {
			continue
		}
		before := map[string]bool{}
		for _, f := range pageTemplateFindings(p, mapName, current, catalog) {
			before[f.Check] = true
		}
		seen := map[string]bool{}
		for _, f := range pageTemplateFindings(p, mapName, after, merged) {
			if before[f.Check] || seen[f.Check] {
				continue
			}
			seen[f.Check] = true
			im.NewFindings++
			if im.ByCheck == nil {
				im.ByCheck = map[string]int{}
			}
			im.ByCheck[f.Check]++
			pages[string(p.id)] = true
		}
	}
	im.Pages = len(pages)
	for id := range pages {
		im.Examples = append(im.Examples, id)
	}
	sort.Strings(im.Examples)
	if len(im.Examples) > proposalMaxExamples {
		im.Examples = im.Examples[:proposalMaxExamples]
	}
	return im
}

// impactClause is the evidence sentence about the pages a proposal would fail.
func impactClause(impact []TemplateImpact) string {
	pages := 0
	byCheck := map[string]int{}
	for _, im := range impact {
		pages += im.Pages
		for c, n := range im.ByCheck {
			byCheck[c] += n
		}
	}
	if pages == 0 {
		return ""
	}
	checks := make([]string, 0, len(byCheck))
	for c := range byCheck {
		checks = append(checks, c)
	}
	sort.Slice(checks, func(i, j int) bool {
		if byCheck[checks[i]] != byCheck[checks[j]] {
			return byCheck[checks[i]] > byCheck[checks[j]]
		}
		return checks[i] < checks[j]
	})
	parts := make([]string, len(checks))
	for i, c := range checks {
		parts[i] = fmt.Sprintf("%s: %d", c, byCheck[c])
	}
	return fmt.Sprintf("declaring it makes %d pages of the map fail (%s)", pages, strings.Join(parts, ", "))
}

// proposedTemplate induces the template text of one slug (D352 decision 7),
// from the pages of every map that would declare it (#687).
func proposedTemplate(g *proposalGroup, contracts map[string]kb.MapContract) string {
	n := len(g.pages)
	// enough reports whether count pages of n clear the optional bar.
	enough := func(count, total int) bool {
		return count >= proposalMinCarriers && float64(count)/float64(total) >= proposalOptionalShare
	}

	// Sections: presence once per page, spelling, position. Heading
	// whitespace is collapsed so a run of spaces is not its own section.
	type sec struct {
		count     int
		positions []int
		spellings map[string]int
	}
	secs := map[string]*sec{}
	for _, p := range g.pages {
		seen := map[string]bool{}
		for pos, h := range h2Lines(p.body) {
			f := foldHeading(h.Text)
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			s := secs[f]
			if s == nil {
				s = &sec{spellings: map[string]int{}}
				secs[f] = s
			}
			s.count++
			s.positions = append(s.positions, pos)
			s.spellings[strings.Join(strings.Fields(h.Text), " ")]++
		}
	}
	type chosen struct {
		fold, name string
		median     int
		count      int
		optional   bool
		aliases    []string
	}
	var picked []chosen
	for f, s := range secs {
		if !enough(s.count, n) {
			continue
		}
		share := float64(s.count) / float64(n)
		sort.Ints(s.positions)
		names := make([]string, 0, len(s.spellings))
		for sp := range s.spellings {
			names = append(names, sp)
		}
		sort.Slice(names, func(i, j int) bool {
			if s.spellings[names[i]] != s.spellings[names[j]] {
				return s.spellings[names[i]] > s.spellings[names[j]]
			}
			return names[i] < names[j]
		})
		c := chosen{fold: f, name: names[0], median: s.positions[(len(s.positions)-1)/2], count: s.count, optional: share < proposalRequiredShare}
		if !strings.ContainsAny(c.name, ":") {
			c.aliases = names[1:]
		}
		picked = append(picked, c)
	}
	sort.Slice(picked, func(i, j int) bool {
		if picked[i].median != picked[j].median {
			return picked[i].median < picked[j].median
		}
		if picked[i].count != picked[j].count {
			return picked[i].count > picked[j].count
		}
		return picked[i].fold < picked[j].fold
	})

	// Fields: presence over all the pages, values per map.
	type fld struct {
		count  int
		values map[string]int
		scalar bool
	}
	collect := func(pages []*reviewConcept) map[string]*fld {
		flds := map[string]*fld{}
		for _, p := range pages {
			for _, key := range p.fm.Keys() {
				if proposalSkipFields[key] || strings.HasPrefix(key, kb.TemplateMetaPrefix) {
					continue
				}
				v, _ := p.fm.Get(key)
				if emptyFrontmatterValue(v) {
					continue
				}
				f := flds[key]
				if f == nil {
					f = &fld{values: map[string]int{}, scalar: true}
					flds[key] = f
				}
				f.count++
				if s, ok := v.(string); ok {
					f.values[strings.TrimSpace(s)]++
				} else {
					f.scalar = false
				}
			}
		}
		return flds
	}
	flds := collect(g.pages)
	perMap := map[string]map[string]*fld{}
	for _, m := range g.maps {
		perMap[m] = collect(g.perMap(m))
	}
	fieldNames := make([]string, 0, len(flds))
	for k := range flds {
		fieldNames = append(fieldNames, k)
	}
	sort.Strings(fieldNames)

	// valuesIn is the vocabulary one map's pages give a field (nil: none).
	valuesIn := func(f *fld, name string, contract kb.MapContract) []string {
		if !f.scalar || proposalNoValueFields[name] || strings.Contains(name, ".") {
			return nil
		}
		allowed, constrained := contract.AllowedValues(g.typ, name)
		var vals []string
		for v := range f.values {
			if constrained && !fieldValueAllowed(allowed, v) {
				continue
			}
			vals = append(vals, v)
		}
		sort.Strings(vals)
		counts := map[string]int{}
		for _, v := range vals {
			counts[v] = f.values[v]
		}
		switch {
		case len(vals) == 0, allDateShaped(counts):
		case !constrained && identifierValued(f.values, f.count):
		case constrained:
			// The map's own vocabulary, not the part of it the pages happen to use.
			out := slices.Clone(allowed)
			sort.Strings(out)
			return slices.Compact(out)
		case len(vals) <= proposalMaxValues && len(vals) < f.count:
			return vals
		}
		return nil
	}

	var required, optional []string
	fieldValues := map[string][]string{}
	for _, name := range fieldNames {
		f := flds[name]
		share := float64(f.count) / float64(n)
		switch {
		case share >= proposalFieldShare:
			required = append(required, name)
		case enough(f.count, n):
			optional = append(optional, name)
		default:
			continue
		}
		// A value list is the template's only when every map that carries
		// the field agrees on it: what differs per map is the map
		// contract's to say (#687).
		var agreed []string
		ok := true
		first := true
		for _, m := range g.maps {
			mf := perMap[m][name]
			if mf == nil {
				continue
			}
			vals := valuesIn(mf, name, contracts[m])
			if first {
				agreed, first = vals, false
			} else if strings.Join(agreed, "\x00") != strings.Join(vals, "\x00") {
				ok = false
				break
			}
		}
		if ok && len(agreed) > 0 {
			fieldValues[name] = agreed
		}
	}

	fm, _ := okf.ParseFrontmatter("")
	fm.Set("type", g.typ)
	fm.Set("title", "{{title}}")
	fm.Set(kb.TemplateMetaDescription, fmt.Sprintf("Proposed from %d %s pages of %s", n, g.typ, mapsPhrase(g.maps)))
	setList := func(key string, vals []string) {
		if len(vals) > 0 {
			fm.Set(key, vals)
		}
	}
	setList(kb.TemplateMetaRequiredFields, required)
	setList(kb.TemplateMetaOptionalFields, optional)
	for _, name := range fieldNames {
		if vals, ok := fieldValues[name]; ok {
			fm.Set(kb.TemplateMetaFieldValues+name, vals)
		}
	}
	var optSecs []string
	for _, c := range picked {
		if c.optional {
			optSecs = append(optSecs, c.name)
		}
	}
	setList(kb.TemplateMetaOptionalSection, optSecs)
	for _, c := range picked {
		setList(kb.TemplateMetaSectionAliases+c.name, c.aliases)
	}

	var body strings.Builder
	body.WriteString("# {{title}}\n")
	for _, c := range picked {
		body.WriteString("\n## " + c.name + "\n")
	}
	return "---\n" + fm.Serialize() + "\n---\n" + body.String()
}

func mapsPhrase(maps []string) string {
	if len(maps) == 1 {
		return "map " + maps[0]
	}
	return fmt.Sprintf("%d maps (%s)", len(maps), strings.Join(maps, ", "))
}
