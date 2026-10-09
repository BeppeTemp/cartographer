package lint

import (
	"fmt"
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
const (
	proposalMinPages      = 3
	proposalRequiredShare = 0.60
	proposalOptionalShare = 0.20
	proposalFieldShare    = 0.90
	proposalMaxValues     = 8
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

type proposalGroup struct {
	mapName string
	typ     string
	pages   []*reviewConcept
}

// templateProposalItems proposes, per map and type, the template its pages
// already follow. A group qualifies with at least proposalMinPages pages in a
// map that declares no templates, or when most of its pages are unbound in a map
// that does. Deterministic: the same KB gives byte-identical items.
func templateProposalItems(concepts []*reviewConcept, contracts map[string]kb.MapContract, catalog kb.TemplateCatalog) []ReviewItem {
	groups := map[[2]string]*proposalGroup{}
	for _, c := range concepts {
		if c.mapName == "" || c.fm == nil || strings.TrimSpace(c.typ) == "" {
			continue
		}
		key := [2]string{c.mapName, c.typ}
		g := groups[key]
		if g == nil {
			g = &proposalGroup{mapName: c.mapName, typ: c.typ}
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

	type proposal struct {
		g    *proposalGroup
		slug string
		text string
		have bool // a template of that slug already exists
	}
	byMap := map[string][]*proposal{}
	var mapOrder []string
	for _, key := range keys {
		g := groups[key]
		contract, ok := contracts[g.mapName]
		if !ok || len(g.pages) < proposalMinPages {
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
		slug := Slugify(g.typ)
		if !kb.ValidTemplateSlug(slug) {
			continue
		}
		pr := &proposal{g: g, slug: slug}
		if _, taken := catalog[slug]; taken {
			pr.have = true
		} else {
			pr.text = proposedTemplate(g, contract)
		}
		if _, seen := byMap[g.mapName]; !seen {
			mapOrder = append(mapOrder, g.mapName)
		}
		byMap[g.mapName] = append(byMap[g.mapName], pr)
	}

	var out []ReviewItem
	for _, name := range mapOrder {
		props := byMap[name]
		slugs := map[string]bool{}
		for _, t := range contracts[name].Templates {
			slugs[t] = true
		}
		var biggest *proposal
		for _, p := range props {
			slugs[p.slug] = true
			if biggest == nil || len(p.g.pages) > len(biggest.g.pages) {
				biggest = p
			}
		}
		all := make([]string, 0, len(slugs))
		for s := range slugs {
			all = append(all, s)
		}
		sort.Strings(all)
		mapUpdate := map[string]interface{}{"map": name, "templates": all, "default_template": biggest.slug, "require_template": true}
		for _, p := range props {
			evidence := fmt.Sprintf("%d %s pages in map %q follow a common structure and bind to no template", len(p.g.pages), p.g.typ, name)
			action := fmt.Sprintf("artifact_write templates/%s.md with the `template` text, then map_update with `map_update`; kb_repair template_missing then binds the pages", p.slug)
			if p.have {
				evidence = fmt.Sprintf("%d %s pages in map %q bind to no template, though templates/%s.md exists", len(p.g.pages), p.g.typ, name, p.slug)
				action = "map_update with `map_update` to declare it for the map; kb_repair template_missing then binds the pages"
			}
			out = append(out, ReviewItem{
				Kind:            ReviewTemplateProposal,
				Concepts:        []string{name + mapDescriptorSuffix},
				Evidence:        evidence,
				SuggestedAction: action,
				Weight:          len(p.g.pages),
				TemplateSlug:    p.slug,
				Template:        p.text,
				MapUpdate:       mapUpdate,
			})
		}
	}
	return out
}

// proposedTemplate induces the template text of one group (D352 decision 7).
func proposedTemplate(g *proposalGroup, contract kb.MapContract) string {
	n := len(g.pages)

	// Sections: presence once per page, spelling, position.
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
			s.spellings[h.Text]++
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
		share := float64(s.count) / float64(n)
		if share < proposalOptionalShare {
			continue
		}
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

	// Fields: presence, then values.
	type fld struct {
		count  int
		values map[string]int
		scalar bool
	}
	flds := map[string]*fld{}
	for _, p := range g.pages {
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
	fieldNames := make([]string, 0, len(flds))
	for k := range flds {
		fieldNames = append(fieldNames, k)
	}
	sort.Strings(fieldNames)
	var required, optional []string
	fieldValues := map[string][]string{}
	for _, name := range fieldNames {
		f := flds[name]
		share := float64(f.count) / float64(n)
		switch {
		case share >= proposalFieldShare:
			required = append(required, name)
		case share >= proposalOptionalShare:
			optional = append(optional, name)
		default:
			continue
		}
		if !f.scalar || proposalNoValueFields[name] || strings.Contains(name, ".") {
			continue
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
		case constrained:
			fieldValues[name] = vals
		case len(vals) <= proposalMaxValues && len(vals) < f.count:
			fieldValues[name] = vals
		}
	}

	fm, _ := okf.ParseFrontmatter("")
	fm.Set("type", g.typ)
	fm.Set("title", "{{title}}")
	fm.Set(kb.TemplateMetaDescription, fmt.Sprintf("Proposed from %d %s pages of map %s", n, g.typ, g.mapName))
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
