// Package docskb generates the documentation KB: a pure function
// of the repository's markdown (README.md, CONTRIBUTING.md, docs/ and
// docs/decisions/) and of a git ref, with no model and no clock, so the same
// input is the same bytes. Pages become expanded concepts whose sections are
// satellites, decisions become leaf concepts, and the two tables of
// docs/index.md become the maps.
package docskb

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/repodocs"
)

// The size caps are variables only so that the tests can lower them and keep
// their fixtures small.
var (
	// softCap is the size above which a section with deeper headings is split.
	softCap = 8 << 10
	// hardCap is the size above which a section without them is cut at blank lines.
	hardCap = 16 << 10
)

const (
	// descMax bounds a description (OKF's free field, kept short for search hits).
	descMax = 200

	repoURL     = "https://github.com/BeppeTemp/cartographer/blob/"
	decisionMap = "decisions"
)

// declared are the pages docs/index.md has no row for, in the maps they
// belong to: the project's own presentation, the index itself and the
// contribution guide. An ordered input of the generator, not a special case.
var declared = []struct {
	src, mapSlug, slug, title, contents string // title: used when the file has no H1
	first                               bool
}{
	{"README.md", "using-cartographer", "readme", "Cartographer", "The project's own presentation: what Cartographer is, its four components and the quick start", true},
	{"docs/index.md", "internals-contributing", "docs-map", "Documentation map", "The documentation map: reading paths and which page owns each kind of change", true},
	{"CONTRIBUTING.md", "internals-contributing", "contributing", "Contributing to Cartographer", "How to contribute: issues, pull requests, plans, releases and the working rules", false},
}

var (
	rowRe      = regexp.MustCompile(`^\| \[([^\]]+)\]\(([^)]+)\) \| (.+) \|$`)
	topicRe    = regexp.MustCompile(`(?m)^topic:[ \t]*(\S.*?)[ \t]*$`)
	decisionRe = regexp.MustCompile(`^(D\d+|AD\d+)-.*\.md$`)
	h1Re       = regexp.MustCompile(`^# (\S.*?)[ \t]*$`)
	deviceRe   = regexp.MustCompile(`^(con|prn|aux|nul|com[1-9]|lpt[1-9])$`)
)

type page struct {
	src, slug, title, contents string
	text                       string // the source, ready to cut
	listsDecisions             bool   // its generated decisions list was replaced by the decisions map
}

type mapDef struct {
	title, slug string
	pages       []page
}

type gen struct {
	out     map[string][]byte
	ref     string
	errs    []error
	targets map[string]target // source file -> the concept it became
	byNum   map[int]string    // decision number -> concept id
}

// Generate returns the KB as a map of slash-separated paths to bytes. src is
// laid out as the repository root: README.md, CONTRIBUTING.md and docs/, so the
// embedded files and os.DirFS of the checkout both work. ref is the git ref the
// pinned source URLs point at.
func Generate(src fs.FS, ref string) (map[string][]byte, error) {
	maps, err := readMaps(src)
	if err != nil {
		return nil, err
	}
	decs, err := readDecisions(src)
	if err != nil {
		return nil, err
	}
	g := &gen{out: map[string][]byte{}, ref: ref, targets: map[string]target{}, byNum: map[int]string{}}
	// Plan every page before writing any, so a link can name a section of a
	// page that comes later.
	var plans []*plan
	for _, m := range maps {
		for _, pg := range m.pages {
			pl := g.plan(m, pg)
			plans = append(plans, pl)
			if pg.listsDecisions { // the list's topic anchors are the topic concepts
				for _, t := range topicsOf(decs) {
					pl.anchors[t.Slug] = topicID(t.Slug)
				}
			}
			g.targets[pg.src] = target{id: pl.id, anchors: pl.anchors}
		}
	}
	for _, d := range decs {
		id := decisionMap + "/" + d.id
		anchors := map[string]string{}
		for _, n := range scan(strings.Split(d.body, "\n")).nodes {
			anchors[n.anchor] = id
		}
		g.targets[d.file] = target{id: id, anchors: anchors}
		if d.num > 0 && strings.HasPrefix(d.id, "d") && !strings.HasPrefix(d.id, "ad") {
			g.byNum[d.num] = id
		}
	}
	for _, pl := range plans {
		g.emitPage(pl)
	}
	g.decisions(decs)
	g.topics(decs)
	g.indexes(maps, decs)
	g.registry()
	if len(g.errs) > 0 {
		return nil, errors.Join(g.errs...)
	}
	return g.out, nil
}

// WriteTree writes files under dir in sorted order, 0644 files in 0755 folders.
func WriteTree(files map[string][]byte, dir string) error {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p := filepath.Join(dir, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, files[n], 0o644); err != nil {
			return err
		}
	}
	return nil
}

func read(src fs.FS, name string) (string, error) {
	b, err := fs.ReadFile(src, name)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n"), nil
}

// readMaps parses the two tables of docs/index.md into maps, adds the declared
// pages and checks that every page file of docs/ is in exactly one place.
func readMaps(src fs.FS) ([]*mapDef, error) {
	raw, err := read(src, "docs/index.md")
	if err != nil {
		return nil, err
	}
	var maps []*mapDef
	var cur *mapDef
	var kept []string
	var f fence
	lines := strings.Split(raw, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if !f.inside(line) {
			if m := headingRe.FindStringSubmatch(line); m != nil && len(m[1]) == 2 {
				cur = &mapDef{title: plainText(m[2]), slug: slug(plainText(m[2]))}
			}
			if strings.TrimSpace(line) == "| File | Contents |" && cur != nil {
				if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "|---") {
					return nil, fmt.Errorf("docs/index.md:%d: the table has no separator row", i+1)
				}
				for i += 2; i < len(lines) && strings.HasPrefix(lines[i], "|"); i++ {
					m := rowRe.FindStringSubmatch(lines[i])
					if m == nil || m[1] != m[2] {
						return nil, fmt.Errorf("docs/index.md:%d: not a `| [file.md](file.md) | contents |` row", i+1)
					}
					cur.pages = append(cur.pages, page{src: "docs/" + m[2], slug: slug(strings.TrimSuffix(m[2], ".md")), contents: m[3]})
				}
				i--
				maps = append(maps, cur)
				kept = append(kept, fmt.Sprintf("The pages of this section are the map `%s` of this KB.", cur.slug))
				continue
			}
		}
		kept = append(kept, line)
	}
	if len(maps) == 0 {
		return nil, fmt.Errorf("docs/index.md: no `| File | Contents |` table")
	}
	for _, d := range declared {
		var m *mapDef
		for _, c := range maps {
			if c.slug == d.mapSlug {
				m = c
			}
		}
		if m == nil {
			return nil, fmt.Errorf("%s: declared in map %q, which docs/index.md has no table for", d.src, d.mapSlug)
		}
		pg := page{src: d.src, slug: d.slug, title: d.title, contents: d.contents}
		if d.first {
			m.pages = append([]page{pg}, m.pages...)
		} else {
			m.pages = append(m.pages, pg)
		}
	}
	seen := map[string]string{}
	for _, m := range maps {
		for i := range m.pages {
			pg := &m.pages[i]
			if prev, dup := seen[m.slug+"/"+pg.slug]; dup {
				return nil, fmt.Errorf("%s: concept id %s/%s is also %s", pg.src, m.slug, pg.slug, prev)
			}
			seen[m.slug+"/"+pg.slug] = pg.src
			seen[pg.src] = pg.src
			text, err := read(src, pg.src)
			if err != nil {
				return nil, fmt.Errorf("%s: listed but unreadable: %w", pg.src, err)
			}
			if pg.src == "docs/index.md" {
				text = strings.Join(kept, "\n")
			}
			pg.listsDecisions = strings.Contains(text, repodocs.IndexBegin)
			pg.text = repodocs.StripGeneratedBlocks(text)
		}
	}
	for _, pat := range []string{"docs/*.md", "docs/brand/*.md"} {
		files, _ := fs.Glob(src, pat)
		for _, f := range files {
			if _, ok := seen[f]; !ok {
				return nil, fmt.Errorf("%s: a docs page in no table of docs/index.md", f)
			}
		}
	}
	return maps, nil
}

func (g *gen) put(name, front, body string) {
	if _, dup := g.out[name]; dup {
		g.errs = append(g.errs, fmt.Errorf("%s: generated twice", name))
	}
	g.out[name] = []byte("---\n" + front + "\n---\n\n" + strings.TrimRight(body, "\n") + "\n")
}

// front serializes frontmatter pairs with the OKF writer, in the order given.
func front(kv ...any) string {
	fm, _ := okf.ParseFrontmatter("")
	for i := 0; i+1 < len(kv); i += 2 {
		if s, ok := kv[i+1].(string); ok && s == "" {
			continue
		}
		fm.Set(kv[i].(string), kv[i+1])
	}
	return fm.Serialize()
}

// label makes text safe inside the alias form [[id|label]].
func label(s string) string { return strings.NewReplacer("|", " ", "]", " ", "[", " ").Replace(s) }

func link(id, title string) string { return "[[" + id + "|" + label(title) + "]]" }

func (g *gen) sourceURL(src, anchor string) string {
	u := repoURL + g.ref + "/" + src
	if anchor != "" {
		u += "#" + anchor
	}
	return u
}

func join(lines []string) string { return strings.Join(lines, "\n") }

func bytesOf(lines []string) int {
	n := 0
	for _, l := range lines {
		n += len(l) + 1
	}
	return n
}

// unit is one section concept, or one part of an oversized one.
type unit struct {
	slug, title, anchor string
	body                []string
	kids                []*unit // sub-sections, linked from this unit
	more                []*unit // parts 2..n of this unit
}

type builder struct {
	title   string
	used    map[string]bool
	anchors map[string]string // heading anchor -> slug of the unit that holds it
}

// hold records that the unit slug holds the heading n and, when it is not split
// below, every heading under it.
func (b *builder) hold(n *node, slug string, deep bool) {
	b.anchors[n.anchor] = slug
	if deep {
		for _, k := range n.kids {
			b.hold(k, slug, true)
		}
	}
}

func (b *builder) fresh(base string, ord int) string {
	if base == "" {
		base = fmt.Sprintf("section-%d", ord)
	}
	if deviceRe.MatchString(base) {
		base += "-section"
	}
	s := base
	for i := 2; b.used[s]; i++ {
		s = fmt.Sprintf("%s-%d", base, i)
	}
	b.used[s] = true
	return s
}

// unit cuts section n: split at its sub-headings while it is above softCap,
// else kept whole, then cut at blank lines when it is above hardCap.
func (b *builder) unit(n *node, path []string) *unit {
	path = append(append([]string(nil), path...), n.plain)
	u := &unit{
		slug:   b.fresh(slug(strings.Join(path, " ")), n.ord),
		title:  b.title + " — " + strings.Join(path, " — "),
		anchor: n.anchor,
	}
	if size(n) > softCap && len(n.kids) > 0 {
		b.hold(n, u.slug, false)
		u.body = trimBlank(n.own)
		for _, k := range n.kids {
			u.kids = append(u.kids, b.unit(k, path))
		}
		return u
	}
	b.hold(n, u.slug, true)
	u.body = trimBlank(render(n))
	if bytesOf(u.body) > hardCap {
		ps := parts(u.body, softCap)
		u.body = ps[0]
		for i, p := range ps[1:] {
			u.more = append(u.more, &unit{slug: b.fresh(fmt.Sprintf("%s-part-%d", u.slug, i+2), n.ord), title: u.title, anchor: n.anchor, body: p})
		}
	}
	return u
}

// plan is a page cut into units, before any link is resolved.
type plan struct {
	m       *mapDef
	pg      page
	id      string
	title   string
	lead    []string
	top     []*unit // nil for a page with no H2
	anchors map[string]string
}

func (g *gen) plan(m *mapDef, pg page) *plan {
	p := scan(strings.Split(pg.text, "\n"))
	title := p.title
	if title == "" {
		title = pg.title
	}
	if title == "" {
		title = pg.slug
	}
	pl := &plan{m: m, pg: pg, id: m.slug + "/" + pg.slug, title: title, lead: trimBlank(p.lead), anchors: map[string]string{}}
	if len(p.h2) == 0 {
		return pl
	}
	b := &builder{title: title, used: map[string]bool{"index": true}, anchors: map[string]string{}}
	for _, n := range p.h2 {
		pl.top = append(pl.top, b.unit(n, nil))
	}
	for a, slug := range b.anchors {
		pl.anchors[a] = pl.id + "/" + slug
	}
	return pl
}

func (g *gen) emitPage(pl *plan) {
	m, pg, id, title := pl.m, pl.pg, pl.id, pl.title
	tags := []string{pg.slug, m.slug}
	pl.anchors[githubAnchor(plainText(title))] = id // the H1's anchor is the page itself
	lead, linked := g.rewrite(pg.src, pg.text, pl.lead)
	citedLine := func(lines []string, self string, linked map[string]bool) string {
		if c := g.cited(lines, self, linked); c != "" {
			return "\n\n" + c
		}
		return ""
	}
	if pl.top == nil {
		g.put("data/"+id+".md", front("type", "Doc", "title", title, "description", firstSentence(pl.lead, descMax), "tags", tags, "provenance", []string{pg.src}, "origin", "external", "source_url", g.sourceURL(pg.src, "")), "# "+title+"\n\n"+join(lead)+citedLine(lead, "", linked))
		return
	}
	var emit func(us []*unit)
	emit = func(us []*unit) {
		for _, u := range us {
			all := append([]*unit{u}, u.more...)
			name := func(i int) string {
				if len(all) == 1 {
					return all[i].title
				}
				return fmt.Sprintf("%s (part %d)", all[i].title, i+1)
			}
			for i, pt := range all {
				text, linked := g.rewrite(pg.src, pg.text, pt.body)
				body := join(text)
				if len(u.kids) > 0 && i == 0 {
					body += "\n\n## Subsections\n\n" + g.list(id, u.kids, false)
				}
				foot := "Part of " + link(id, title) + "."
				if len(all) > 1 {
					foot += fmt.Sprintf(" Part %d of %d.", i+1, len(all))
					if i > 0 {
						foot += " Previous: " + link(id+"/"+all[i-1].slug, name(i-1)) + "."
					}
					if i+1 < len(all) {
						foot += " Next: " + link(id+"/"+all[i+1].slug, name(i+1)) + "."
					}
				}
				g.put("data/"+id+"/"+pt.slug+".md", front("type", "DocSection", "title", name(i), "description", firstSentence(pt.body, descMax), "tags", tags, "provenance", []string{pg.src}, "origin", "external", "source_url", g.sourceURL(pg.src, u.anchor)),
					"# "+name(i)+"\n\n"+strings.TrimSpace(body)+"\n\n"+foot+citedLine(text, "", linked))
			}
			emit(u.kids)
		}
	}
	emit(pl.top)
	body := "# " + title + "\n\n"
	if len(lead) > 0 {
		body += join(lead) + "\n\n"
	}
	body += "## Sections\n\n" + g.list(id, pl.top, true)
	g.put("data/"+id+"/index.md", front("type", "Doc", "title", title, "description", firstSentence(pl.lead, descMax), "tags", tags, "provenance", []string{pg.src}, "origin", "external", "source_url", g.sourceURL(pg.src, "")), body+citedLine(lead, "", linked))
}

// list is the bullet list of units: id, title and first sentence. The page
// index passes deep to list every sub-section too, indented under its parent,
// because the index of an expanded concept must name all its satellites.
func (g *gen) list(pageID string, us []*unit, deep bool) string {
	return strings.Join(g.bullets(pageID, us, deep, ""), "\n")
}

func (g *gen) bullets(pageID string, us []*unit, deep bool, indent string) []string {
	var out []string
	for _, u := range us {
		all := append([]*unit{u}, u.more...)
		for i, pt := range all {
			t := pt.title
			if len(all) > 1 {
				t = fmt.Sprintf("%s (part %d)", t, i+1)
			}
			line := indent + "- " + link(pageID+"/"+pt.slug, t)
			if d := firstSentence(pt.body, descMax); d != "" {
				line += " — " + d
			}
			out = append(out, line)
		}
		if deep {
			out = append(out, g.bullets(pageID, u.kids, deep, indent+"  ")...)
		}
	}
	return out
}

type decision struct {
	id, title, topic, desc string
	num                    int
	file, body             string
}

func readDecisions(src fs.FS) ([]decision, error) {
	entries, err := fs.ReadDir(src, "docs/decisions")
	if err != nil {
		return nil, err
	}
	topics := map[string]bool{}
	for _, t := range repodocs.TopicOrder {
		topics[t.Slug] = true
	}
	var out []decision
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || name == "TEMPLATE.md" || strings.HasPrefix(name, "D-new-") {
			continue
		}
		file := "docs/decisions/" + name
		if !decisionRe.MatchString(name) {
			return nil, fmt.Errorf("%s: name is neither D<n>-<slug>.md nor AD*.md", file)
		}
		text, err := read(src, file)
		if err != nil {
			return nil, err
		}
		front, rest, ok := strings.Cut(strings.TrimPrefix(text, "---\n"), "\n---\n")
		if !strings.HasPrefix(text, "---\n") || !ok {
			return nil, fmt.Errorf("%s: no frontmatter", file)
		}
		m := topicRe.FindStringSubmatch(front)
		if m == nil || !topics[m[1]] {
			return nil, fmt.Errorf("%s: missing or unknown `topic:` in frontmatter", file)
		}
		d := decision{topic: m[1], file: file, id: strings.ToLower(strings.TrimSuffix(name, ".md"))}
		lines := strings.Split(strings.TrimLeft(rest, "\n"), "\n")
		var f fence
		for i, l := range lines {
			if h := h1Re.FindStringSubmatch(l); !f.inside(l) && h != nil {
				d.title = h[1]
				d.desc = firstSentence(lines[i+1:], descMax)
				break
			}
		}
		if d.title == "" {
			return nil, fmt.Errorf("%s: no `# ` heading", file)
		}
		fmt.Sscanf(strings.TrimPrefix(name, "D"), "%d", &d.num)
		d.body = join(lines)
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].num != out[j].num {
			return out[i].num < out[j].num
		}
		return out[i].file < out[j].file
	})
	return out, nil
}

func (g *gen) decisions(ds []decision) {
	for _, d := range ds {
		lines, linked := g.rewrite(d.file, d.body, strings.Split(d.body, "\n"))
		body := strings.TrimRight(join(lines), "\n") + "\n\nTopic: " + link(topicID(d.topic), topicTitle(d.topic)) + "."
		if c := g.cited(lines, decisionMap+"/"+d.id, linked); c != "" {
			body += "\n\n" + c
		}
		g.put("data/"+decisionMap+"/"+d.id+".md", front("type", "Decision", "title", d.title, "description", d.desc, "tags", []string{d.topic}, "provenance", []string{d.file}, "origin", "external", "source_url", g.sourceURL(d.file, "")), body)
	}
}

func topicID(slug string) string { return decisionMap + "/topic-" + slug }

func topicTitle(slug string) string {
	for _, t := range repodocs.TopicOrder {
		if t.Slug == slug {
			return t.Title
		}
	}
	return slug
}

// topicsOf is the topics that have at least one decision, in TopicOrder.
func topicsOf(ds []decision) []struct{ Slug, Title string } {
	var out []struct{ Slug, Title string }
	for _, t := range repodocs.TopicOrder {
		for _, d := range ds {
			if d.topic == t.Slug {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// topics writes one concept per topic, listing its decisions: what the anchors
// of the dropped decisions list stand for, and the one link every decision has.
func (g *gen) topics(ds []decision) {
	for _, t := range topicsOf(ds) {
		desc := "The decisions recorded under " + t.Title + "."
		body := "# " + t.Title + "\n\n" + desc + "\n\n"
		for _, d := range ds {
			if d.topic == t.Slug {
				body += "- " + link(decisionMap+"/"+d.id, d.title) + "\n"
			}
		}
		g.put("data/"+topicID(t.Slug)+".md", front("type", "DecisionTopic", "title", t.Title, "description", desc, "tags", []string{t.Slug}, "provenance", []string{"docs/decisions"}, "origin", "external"), body)
	}
}

// indexes writes the maps' curated indexes, the root index and the directive.
func (g *gen) indexes(maps []*mapDef, ds []decision) {
	root := "# Cartographer documentation\n\nThe documentation of Cartographer " + g.ref + " as a knowledge base.\n\n"
	for _, m := range maps {
		desc := fmt.Sprintf("The pages of the documentation under %q.", m.title)
		body := "# " + m.title + "\n\n" + desc + "\n\n"
		for _, pg := range m.pages {
			body += fmt.Sprintf("- [[%s/%s]] — %s\n", m.slug, pg.slug, pg.contents)
		}
		g.mapFiles(m.slug, m.title, desc, "[Doc, DocSection]", body)
		root += fmt.Sprintf("- [%s](%s/index.md) — %s\n", m.title, m.slug, desc)
	}
	desc := "One record per decision: why each choice was made."
	body := "# Decisions\n\n" + desc + "\n"
	for _, t := range repodocs.TopicOrder {
		first := true
		for _, d := range ds {
			if d.topic != t.Slug {
				continue
			}
			if first {
				body += "\n### " + t.Title + "\n\n"
				body += fmt.Sprintf("- [[%s]] — all the decisions of this topic\n", topicID(t.Slug))
				first = false
			}
			body += fmt.Sprintf("- [[%s/%s]] — %s\n", decisionMap, d.id, d.title)
		}
	}
	g.mapFiles(decisionMap, "Decisions", desc, "[Decision, DecisionTopic]", body)
	root += "- [Decisions](" + decisionMap + "/index.md) — " + desc + "\n"
	g.put("data/index.md", front("type", "Index", "title", "Cartographer documentation"), root)
	g.put("instructions.md", front("perimeter", "docs"), "# How to use this KB\n\nThis is the documentation of Cartographer "+g.ref+", written as a knowledge base: ask it how Cartographer works and why.\n"+
		"Search it, read the concepts it returns and cite the concept ids you answer from. A page is a concept and its sections are `<page>/<section>`; a decision is `decisions/d<n>-<slug>`.\n")
}

func (g *gen) mapFiles(slug, title, desc, types, index string) {
	g.put(path.Join("data", slug, "_map.md"), front("type", "Map", "title", title, "description", desc, "kind", "map", "concept_types", strings.Fields(strings.NewReplacer("[", "", "]", "", ",", "").Replace(types))),
		"# "+title+"\n\n"+desc)
	g.put(path.Join("data", slug, "index.md"), front("type", "Index", "title", title), index)
}
