// Package testkbs generates the synthetic test Knowledge Bases `make dev`
// serves (D818): ten KBs from 5,000 concepts down to 30, the scale the Atlas
// has to handle well, each built to put every Atlas view in a state worth
// looking at — Maps with every contract key, work in every status, sources,
// contradictions, services, secrets, artifacts, placeholders, planted lint
// defects, a git history with revisions, moves and repairs, and the local
// state (lint trend, search misses, conflicts, usage) the server keeps beside
// the repository. The smallest KB has no git and no artifacts, for the empty
// states.
//
// The output is a pure function of the spec and the day it is generated for:
// a seeded generator, no clock read here. The content is invented and neutral,
// so nothing in it names a real system or person.
package testkbs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Spec is one test KB.
type Spec struct {
	Name     string
	Concepts int
	Seed     uint32
	// Bare leaves out git, artifacts, services, paths and instructions: the
	// KB the empty states are judged on.
	Bare bool
	// Remote is set as the origin remote (never contacted: the dev server
	// runs with git sync off), so Settings shows a KB with a remote.
	Remote string
	// Conflicts plants unresolved git conflicts in the local state.
	Conflicts bool
}

// Sizes are the concept counts of the fleet, largest first.
var Sizes = []int{5000, 3000, 2000, 1200, 800, 500, 300, 150, 80, 30}

// Fleet returns the ten test KBs.
func Fleet() []Spec {
	out := make([]Spec, 0, len(Sizes))
	for i, n := range Sizes {
		s := Spec{Name: fmt.Sprintf("test-%d", n), Concepts: n, Seed: uint32(7 + i*31)}
		switch n {
		case 3000:
			s.Remote = "https://gitlab.example.com/kb/test-3000.git"
		case 2000:
			s.Conflicts = true
		case 30:
			s.Bare = true
		}
		out = append(out, s)
	}
	return out
}

// GenerateFleet writes every KB of the fleet under dataDir, one directory
// each, in parallel.
func GenerateFleet(dataDir string, day time.Time) error {
	specs := Fleet()
	errs := make([]error, len(specs))
	var wg sync.WaitGroup
	for i, s := range specs {
		wg.Add(1)
		go func(i int, s Spec) {
			defer wg.Done()
			errs[i] = Generate(filepath.Join(dataDir, s.Name), s, day)
		}(i, s)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// rng is a small seeded LCG: the same seed always yields the same KB.
type rng struct{ s uint32 }

func (r *rng) next() uint32 { r.s = r.s*1664525 + 1013904223; return r.s }
func (r *rng) f() float64   { return float64(r.next()) / (1 << 32) }
func (r *rng) n(k int) int  { return int(r.f() * float64(k)) }
func (r *rng) chance(p float64) bool {
	return r.f() < p
}

// domains name the topical Maps; each word seeds concept titles.
var domains = []struct {
	slug, title string
	words       []string
}{
	{"cartography", "Cartography", []string{"projection", "meridian", "survey", "contour", "scale", "legend", "datum", "bearing", "grid", "relief"}},
	{"botany", "Botany", []string{"fern", "lichen", "moss", "pine", "oak", "sedge", "root", "seed", "pollen", "bark"}},
	{"music", "Music", []string{"cadence", "fugue", "motif", "timbre", "tempo", "chord", "canon", "rondo", "scale", "drone"}},
	{"geology", "Geology", []string{"strata", "basalt", "fault", "glacier", "delta", "moraine", "quartz", "fold", "schist", "dune"}},
	{"language", "Language", []string{"syntax", "idiom", "phoneme", "lexicon", "dialect", "grammar", "metre", "glyph", "accent", "clause"}},
	{"weather", "Weather", []string{"front", "monsoon", "cirrus", "gust", "drizzle", "isobar", "squall", "haze", "dew", "frost"}},
	{"craft", "Craft", []string{"loom", "kiln", "chisel", "lathe", "glaze", "weave", "joinery", "forge", "dye", "rivet"}},
	{"oceans", "Oceans", []string{"tide", "current", "reef", "trench", "swell", "kelp", "plankton", "gyre", "shoal", "brine"}},
	{"chemistry", "Chemistry", []string{"catalyst", "isotope", "polymer", "solvent", "buffer", "alloy", "ion", "enzyme", "crystal", "acid"}},
	{"architecture", "Architecture", []string{"arch", "vault", "lintel", "buttress", "facade", "atrium", "column", "dome", "truss", "plinth"}},
	{"cooking", "Cooking", []string{"broth", "roux", "brine", "crumb", "glaze", "ferment", "simmer", "knead", "zest", "sear"}},
	{"rail", "Railways", []string{"gauge", "signal", "junction", "siding", "bogie", "viaduct", "switch", "ballast", "platform", "tender"}},
	{"aviation", "Aviation", []string{"aileron", "flap", "glide", "stall", "runway", "rudder", "pitch", "yaw", "thrust", "wing"}},
	{"typography", "Typography", []string{"serif", "kerning", "ligature", "baseline", "glyph-set", "leading", "em-dash", "ascender", "folio", "gutter"}},
	{"textiles", "Textiles", []string{"warp", "weft", "felt", "linen", "twill", "denier", "selvedge", "bobbin", "nap", "yarn"}},
	{"mathematics", "Mathematics", []string{"lemma", "matrix", "vector", "prime", "series", "limit", "graph", "group", "ring", "field"}},
	{"ecology", "Ecology", []string{"niche", "biome", "predator", "succession", "habitat", "canopy", "wetland", "keystone", "food-web", "corridor"}},
	{"optics", "Optics", []string{"lens", "prism", "focus", "aperture", "diffraction", "mirror", "filter", "spectrum", "refraction", "glare"}},
	{"astronomy", "Astronomy", []string{"orbit", "comet", "nebula", "parallax", "zenith", "eclipse", "quasar", "transit", "albedo", "pulsar"}},
	{"logistics", "Logistics", []string{"pallet", "depot", "manifest", "route", "dock", "freight", "buffer-stock", "lead-time", "carrier", "bin"}},
}

var (
	authors     = []string{"curator", "researcher", "field-notes"}
	agents      = []string{"claude", "codex", "kiro"}
	sourceKinds = []string{"paper", "dataset", "web page", "interview"}
	taskStates  = []string{"open", "open", "in-progress", "blocked", "decision-needed", "proposed", "done", "done"}
)

// file is one file of the KB at every revision it goes through: versions[0]
// is born at commit born, versions[i] lands at revs[i-1].
type file struct {
	path     string
	versions []string
	born     int
	revs     []int
	// movedFrom is the path the file was born at, renamed into path at moveAt.
	movedFrom string
	moveAt    int
	// deleteAt removes the file at that commit (0: kept).
	deleteAt int
	// reason is the Reason: trailer of the commits that touch it.
	reason string
}

type commit struct {
	subject, reason, agent, author string
	at                             time.Time
}

// unborn marks a file whose birth commit history() has not chosen yet.
const unborn = -1

type kbgen struct {
	spec    Spec
	r       *rng
	day     time.Time
	files   []*file
	byPath  map[string]*file
	commits []commit
	maps    []mapInfo
	ids     []string // every topical concept id
	retired []string
}

type mapInfo struct {
	slug, title string
	words       []string
	ids         []string
}

// Generate writes one test KB at root, replacing whatever is there.
func Generate(root string, s Spec, day time.Time) error {
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	g := &kbgen{spec: s, r: &rng{s: s.Seed}, day: day.UTC().Truncate(24 * time.Hour), byPath: map[string]*file{}}
	g.plan()
	if s.Bare {
		return g.writeFinal(root)
	}
	if err := g.replay(root); err != nil {
		return fmt.Errorf("testkbs: %s: %w", s.Name, err)
	}
	return g.localState(root)
}

func (g *kbgen) add(path, content string, born int) *file {
	f := &file{path: path, versions: []string{content}, born: born}
	g.files = append(g.files, f)
	g.byPath[path] = f
	return f
}

// commitAt registers a commit and returns its index; at spreads commits over
// the last 180 days, the newest ones inside the last week.
func (g *kbgen) commitAt(subject, reason string, frac float64) int {
	at := g.day.Add(-time.Duration((1-frac)*180*24) * time.Hour).Add(time.Duration(9+g.r.n(10)) * time.Hour)
	if at.After(g.day) {
		at = g.day.Add(-time.Hour)
	}
	c := commit{subject: subject, reason: reason, author: authors[g.r.n(len(authors))]}
	if g.r.chance(0.6) {
		c.agent = agents[g.r.n(len(agents))]
	}
	c.at = at
	g.commits = append(g.commits, c)
	return len(g.commits) - 1
}

func fm(fields ...string) string {
	var b strings.Builder
	b.WriteString("---\n")
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i+1] == "" {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", fields[i], fields[i+1])
	}
	b.WriteString("---\n")
	return b.String()
}

func title(word string) string {
	w := strings.ReplaceAll(word, "-", " ")
	return strings.ToUpper(w[:1]) + w[1:]
}

func list(xs ...string) string { return "[" + strings.Join(xs, ", ") + "]" }

func links(ids []string) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = "[[" + id + "]]"
	}
	return strings.Join(parts, ", ")
}

func (g *kbgen) date(daysAgo int) string {
	return g.day.AddDate(0, 0, -daysAgo).Format("2006-01-02")
}

// plan lays out every file and every commit.
func (g *kbgen) plan() {
	n := g.spec.Concepts
	nMaps := n / 250
	if nMaps < 3 {
		nMaps = 3
	}
	if nMaps > len(domains) {
		nMaps = len(domains)
	}
	scaffold := g.commitAt("Scaffold the knowledge base", "", 0)

	// What is not topical: work, journal, sources, open questions.
	nTasks := max(4, n/40)
	nNotes := max(3, n/50)
	nSources := max(3, n/60)
	nQuestions := max(2, n/300)
	nTopical := n - nTasks - nNotes - nSources - nQuestions
	if nTopical < 6*nMaps {
		nTopical = 6 * nMaps
	}

	for i := 0; i < nMaps; i++ {
		d := domains[i]
		g.maps = append(g.maps, mapInfo{slug: d.slug, title: d.title, words: d.words})
	}
	// Concept ids, spread over the Maps; one ring of six in the first Map is
	// linked only to itself (the island).
	const island = 6
	for i := 0; i < nTopical-island; i++ {
		m := &g.maps[i%nMaps]
		k := len(m.ids)
		word := m.words[k%len(m.words)]
		id := m.slug + "/" + word
		if k >= len(m.words) {
			id = fmt.Sprintf("%s-%d", id, k/len(m.words)+1)
		}
		m.ids = append(m.ids, id)
	}
	for i := 1; i <= island; i++ {
		g.maps[0].ids = append(g.maps[0].ids, fmt.Sprintf("%s/island-%d", g.maps[0].slug, i))
	}
	for _, m := range g.maps {
		g.ids = append(g.ids, m.ids...)
	}

	g.scaffold(scaffold, nMaps)
	g.topical(nTopical)
	g.work(nTasks)
	g.journal(nNotes)
	g.sources(nSources)
	g.questions(nQuestions)
	g.defects()
	g.indexes(scaffold)
	if !g.spec.Bare {
		g.artifacts(scaffold)
	}
	g.history()
}

// scaffold writes the root files and the Map contracts.
func (g *kbgen) scaffold(c, nMaps int) {
	g.add("data/index.md", fm("type", "Index", "title", "Test KB "+g.spec.Name)+"\n# Test KB "+g.spec.Name+"\n\nA synthetic knowledge base generated to exercise every view of the Atlas.\n", c)
	var log strings.Builder
	log.WriteString("# Log\n")
	entries := max(4, min(200, g.spec.Concepts/25))
	for i := entries; i > 0; i-- {
		at := g.day.Add(-time.Duration(i*180*24/entries) * time.Hour).Add(10 * time.Hour)
		text := fmt.Sprintf("Session: filed %d new concepts and reviewed the open work.", 3+g.r.n(20))
		if i%9 == 0 {
			text = "kb-doctor session: worked through the review list, fixed the index and two broken links."
		}
		fmt.Fprintf(&log, "\n## %s\n\n%s\n", at.Format(time.RFC3339), text)
	}
	g.add("data/log.md", log.String(), c)
	if !g.spec.Bare {
		g.add("instructions.md", "# House rules\n\nFile every finding under the Map that owns its subject, and cite its source.\n", c)
		g.add("settings.yaml", "write_gate: warning\ndoctor_interval: 7d\nusage_stale_days: 21\n", c)
		g.add("glossary.yaml", "terms:\n  - canonical: knowledge base\n    aliases: [KB]\n  - canonical: field survey\n    aliases: [survey trip]\n    forbidden: [site walk]\n", c)
		g.add("templates/task.md", fm("type", "Task", "title", `"{{title}}"`, "status", "open", "x-template.description", "A piece of work with an owner and a definition of done", "x-template.required_fields", list("status"))+"\n# {{title}}\n\n## Context\n\n## Definition of done\n", c)
		g.add("templates/note.md", fm("type", "Note", "title", `"{{title}}"`, "x-template.description", "A dated journal note")+"\n# {{title}}\n\n## What happened\n\n## Follow-ups\n", c)
		g.add("templates/topic.md", fm("type", "Topic", "title", `"{{title}}"`, "description", `"{{description}}"`, "x-template.description", "A subject explained once", "x-template.required_fields", list("description"))+"\n# {{title}}\n\n## Overview\n\n## Details\n\n## See also\n", c)
	}

	// The topical Maps, each with a different part of the contract, so the
	// contract panel has every key to show somewhere.
	for i, m := range g.maps {
		f := []string{"type", "Map", "title", m.title, "description", "Concepts about " + strings.ToLower(m.title) + ".", "kind", "map"}
		// The malformed contract comes second, so even the smallest KB has it.
		switch []int{0, 4, 1, 2, 3, 5}[i%6] {
		case 0:
			f = append(f, "concept_types", list("Topic", "Entity", "Note"), "ontology_mode", "strict",
				"required_fields", list("description"), "field_values.status", list("active", "draft", "deprecated", "superseded", "archived"),
				"templates", list("topic"), "default_template", "topic", "require_template", "false", "template_sections", "true",
				"title_max_length", "60", "forbidden_title_terms", list("misc", "untitled"))
		case 1:
			f = append(f, "ontology_mode", "flexible", "open_statuses", list("open", "draft"), "open_markers", list("TODO", "FIXME"),
				"stale_after", "45", "work_map", "work", "lint_ignore", list("map_without_templates"))
		case 2:
			f = append(f, "ontology_mode", "flexible", "hotspot_in_degree", "40", "oversize_bytes", "60000", "repeated_fact_min", "4",
				"value_synonyms.active", list("live", "current"), "machine_path_allow_prefixes", list("/srv/shared"), "index", "generated")
		case 3:
			f = append(f, "ontology_mode", "flexible", "forbidden_fields", list("owner_email"), "required_fields.Entity", list("description"),
				"field_values.Entity.status", list("active", "deprecated"), "require_index_entry", "true", "procedure_headings", list("Steps", "Rollback"))
		case 4:
			// One malformed key: the panel's malformed row and contract_malformed.
			f = append(f, "ontology_mode", "flexible", "stale_after", "soon", "oversize_concepts", "400", "hotspot_bytes", "20000")
		default:
			f = append(f, "ontology_mode", "flexible")
		}
		f = append(f, "timestamp", g.date(180))
		g.add("data/"+m.slug+"/_map.md", fm(f...)+"\n# "+m.title+"\n", c)
	}
	g.add("data/work/_map.md", fm("type", "Map", "title", "Work", "description", "Tasks and decisions, with their status.", "kind", "map",
		"concept_types", list("Task", "Decision"), "ontology_mode", "strict",
		"field_values.status", list(taskStates[0], "in-progress", "blocked", "decision-needed", "proposed", "done"),
		"open_statuses", list("open", "in-progress", "blocked", "decision-needed", "proposed"),
		"templates", list("task"), "default_template", "task", "require_template", "true", "stale_after", "30", "timestamp", g.date(180))+"\n# Work\n", c)
	g.add("data/journal/_map.md", fm("type", "Map", "title", "Journal", "description", "Dated notes from working sessions.", "kind", "journal",
		"concept_types", list("Note"), "promote_to", g.maps[0].slug, "harvest_after", "30", "open_markers", list("TODO"),
		"templates", list("note"), "timestamp", g.date(180))+"\n# Journal\n", c)
	g.add("data/sources/_map.md", fm("type", "Map", "title", "Sources", "description", "The source ledger.", "kind", "journal",
		"concept_types", list("Source"), "ontology_mode", "strict",
		"field_values.ingest_status", list("pending", "ingested", "skipped"), "field_values.source_kind", list(sourceKinds...),
		"lint_ignore", list("map_without_templates"), "timestamp", g.date(180))+"\n# Sources\n", c)
	g.add("data/questions/_map.md", fm("type", "Map", "title", "Open Questions", "description", "Where the sources disagree, and what is not known yet.", "kind", "map",
		"concept_types", list("Contradiction"), "lint_ignore", list("map_without_templates"), "timestamp", g.date(180))+"\n# Open Questions\n", c)
	_ = nMaps
}

// topical writes the bulk of the graph: clustered Maps with hubs, bridges,
// orphans, the island, supersede pairs and retired concepts.
func (g *kbgen) topical(nTopical int) {
	hubs := make([]string, len(g.maps))
	for i, m := range g.maps {
		hubs[i] = m.ids[0]
	}
	nSources := max(3, g.spec.Concepts/60)
	for mi, m := range g.maps {
		normal := m.ids
		islandStart := len(m.ids)
		if mi == 0 {
			islandStart = len(m.ids) - 6
			normal = m.ids[:islandStart]
		}
		for k, id := range m.ids {
			var out []string
			isIsland := k >= islandStart
			switch {
			case isIsland:
				j := k - islandStart
				out = append(out, m.ids[islandStart+(j+1)%6])
				if j == 0 {
					out = append(out, m.ids[islandStart+3])
				}
			case g.r.chance(0.03):
				// an orphan-ish leaf: links out to nothing
			default:
				if id != hubs[mi] && g.r.chance(0.55) {
					out = append(out, hubs[mi])
				}
				for c := 0; c < 1+g.r.n(3); c++ {
					if o := normal[g.r.n(len(normal))]; o != id {
						out = append(out, o)
					}
				}
				if g.r.chance(0.1) {
					out = append(out, hubs[g.r.n(len(hubs))])
				}
				if g.r.chance(0.002) {
					out = append(out, m.slug+"/missing-"+fmt.Sprint(k)) // a broken link
				}
			}
			out = dedupe(out, id)

			typ := []string{"Topic", "Topic", "Entity", "Note"}[g.r.n(4)]
			status := "active"
			switch x := g.r.f(); {
			case x < 0.08:
				status = "draft"
			case x < 0.09:
				status = "deprecated"
			case x < 0.095:
				status = "archived"
			}
			word := id[strings.LastIndex(id, "/")+1:]
			t := title(word)
			fields := []string{"type", typ, "title", t, "description", t + ", as the " + strings.ToLower(m.title) + " Map uses the term.", "status", status, "timestamp", g.date(g.r.n(170))}
			if g.r.chance(0.15) {
				fields = append(fields, "provenance", list(fmt.Sprintf("sources/source-%d", 1+g.r.n(nSources))))
			}
			if g.r.chance(0.01) {
				fields = append(fields, "review_after", g.date(5+g.r.n(30)))
			}
			body := g.body(t, m.title, out, k%7 == 0)
			f := g.add("data/"+id+".md", fm(fields...)+body, unborn)
			if status == "deprecated" || status == "archived" {
				g.retired = append(g.retired, id)
				f.reason = "retired: no longer in use"
			}
		}
	}
	// Supersede pairs: the old concept points at its successor.
	pairs := max(1, g.spec.Concepts/200)
	for p := 0; p < pairs; p++ {
		m := g.maps[p%len(g.maps)]
		if 2+p/len(g.maps) >= len(m.ids)-6 {
			continue
		}
		oldID, newID := m.ids[2+p/len(g.maps)], m.ids[1]
		if strings.Contains(oldID, "island-") {
			continue
		}
		f := g.byPath["data/"+oldID+".md"]
		if f == nil || oldID == newID || f.reason != "" {
			continue
		}
		t := title(oldID[strings.LastIndex(oldID, "/")+1:])
		f.versions = append(f.versions, fm("type", "Topic", "title", t, "description", "Replaced by "+newID+".", "status", "superseded", "superseded_by", newID, "timestamp", g.date(20))+
			"\n# "+t+"\n\nSuperseded: read [["+newID+"]] instead.\n")
		f.reason = "supersede: " + oldID + " -> " + newID
	}
	// Expanded concepts with assets, one per few Maps.
	for i := 0; i < len(g.maps); i += 3 {
		m := g.maps[i]
		id := m.slug + "/field-kit"
		g.ids = append(g.ids, id)
		g.maps[i].ids = append(g.maps[i].ids, id)
		g.add("data/"+id+"/index.md", fm("type", "Entity", "title", "Field kit", "description", "What to carry, with a checklist and a diagram.", "status", "active", "timestamp", g.date(40))+
			"\n# Field kit\n\nSee the checklist `checklist.csv` and the layout `layout.svg`. Belongs to [["+m.ids[0]+"]].\n", unborn)
		g.add("data/"+id+"/checklist.csv", "item,qty\nnotebook,1\npencil,3\nmap,1\n", unborn)
		g.add("data/"+id+"/layout.svg", `<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20"><rect width="40" height="20" fill="#888"/></svg>`+"\n", unborn)
		if i == 0 {
			g.add("data/"+id+"/unused-photo.txt", "an asset nothing references\n", unborn)
		}
	}
	_ = nTopical
}

func dedupe(ids []string, self string) []string {
	seen := map[string]bool{self: true}
	out := ids[:0]
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// body is a concept's text; long ones get three headings, so the reading
// panel shows its table of contents.
func (g *kbgen) body(t, mapTitle string, out []string, long bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n# %s\n\n%s is a test concept of the %s Map.", t, t, strings.ToLower(mapTitle))
	if g.r.chance(0.05) {
		b.WriteString(" The shared folder is {{path:shared-drive}}; the tooling lives in {{repo:tooling}}.")
	}
	b.WriteString("\n")
	if long {
		b.WriteString("\n## Overview\n\nWhat it is and where it is used.\n\n## Details\n\nHow it behaves, step by step.\n\n## Caveats\n\nWhere it breaks down.\n")
	}
	if len(out) > 0 {
		fmt.Fprintf(&b, "\nIt is used together with %s, which explain where it fits.\n", links(out))
	} else {
		b.WriteString("\nIt links to nothing yet.\n")
	}
	return b.String()
}

// work writes Tasks in every status: open, in progress, blocked, a decision
// to take, proposed, done (one done with an open checkbox), stale ones.
func (g *kbgen) work(n int) {
	for i := 0; i < n; i++ {
		status := taskStates[i%len(taskStates)]
		id := fmt.Sprintf("work/task-%03d", i+1)
		age := g.r.n(100)
		fields := []string{"type", "Task", "title", fmt.Sprintf("Task %d: review the %s entries", i+1, g.maps[i%len(g.maps)].title), "status", status, "priority", fmt.Sprintf("P%d", i%4), "timestamp", g.date(age)}
		if status == "blocked" {
			fields = append(fields, "waiting_on", "an answer from the survey team", "review_after", g.date(-7))
		}
		if status == "decision-needed" {
			fields[1] = "Decision"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "\n# Task %d\n\nKeep [[%s]] accurate.\n\n## Definition of done\n\n", i+1, g.maps[i%len(g.maps)].ids[0])
		if status == "done" && i%2 == 0 {
			b.WriteString("- [x] Re-read the entries\n- [ ] Tell the readers\n") // closed_with_open_items
		} else if status != "done" {
			b.WriteString("- [x] Collect the entries\n- [ ] Fix the broken ones\n- [ ] Update the index\n")
		} else {
			b.WriteString("- [x] Everything\n")
		}
		g.add("data/"+id+".md", fm(fields...)+b.String(), unborn)
	}
}

// journal writes dated notes, some with open markers and checkboxes.
func (g *kbgen) journal(n int) {
	for i := 0; i < n; i++ {
		ago := (n - i) * 170 / n
		id := fmt.Sprintf("journal/%s-session-%d", g.date(ago), i+1)
		var b strings.Builder
		fmt.Fprintf(&b, "\n# Session %d\n\nWorked on [[%s]].\n", i+1, g.ids[g.r.n(len(g.ids))])
		if i%4 == 0 {
			b.WriteString("\nTODO: promote the findings to the Map.\n\n## Follow-ups\n\n- [ ] Promote the findings\n")
		}
		g.add("data/"+id+".md", fm("type", "Note", "title", fmt.Sprintf("Session %d", i+1), "status", "active", "timestamp", g.date(ago))+b.String(), unborn)
	}
}

// sources writes the source ledger: ingested, pending, skipped; one uncited.
func (g *kbgen) sources(n int) {
	for i := 1; i <= n; i++ {
		st := []string{"ingested", "ingested", "pending", "skipped"}[i%4]
		g.add(fmt.Sprintf("data/sources/source-%d.md", i), fm("type", "Source", "title", fmt.Sprintf("Survey report %d", i),
			"source_kind", sourceKinds[i%len(sourceKinds)], "locator", fmt.Sprintf("https://example.com/reports/%d", i),
			"ingest_status", st, "timestamp", g.date(170-i%170))+fmt.Sprintf("\n# Survey report %d\n\nA report the knowledge base was built from.\n", i), unborn)
	}
}

// questions writes contradictions (open and resolved) and gaps.
func (g *kbgen) questions(n int) {
	kinds := []string{"measurement", "definition", "open_question", "missing_context"}
	for i := 1; i <= n; i++ {
		k := kinds[(i-1)%len(kinds)]
		res := "open"
		if i%5 == 0 {
			res = "resolved"
		}
		a, b := g.ids[g.r.n(len(g.ids))], g.ids[g.r.n(len(g.ids))]
		g.add(fmt.Sprintf("data/questions/question-%d.md", i), fm("type", "Contradiction", "title", fmt.Sprintf("Question %d", i),
			"description", "Two concepts disagree, or something is not known.", "contradiction_kind", k, "resolution_status", res,
			"involves", list(a, b), "reason", "The two values do not overlap.", "status", "active", "timestamp", g.date(g.r.n(90)))+
			fmt.Sprintf("\n# Question %d\n\n[[%s]] and [[%s]] do not agree.\n", i, a, b), unborn)
	}
}

// defects plants one of each lint finding the Health view groups.
func (g *kbgen) defects() {
	m0, m1 := g.maps[0], g.maps[1%len(g.maps)]
	g.add("data/"+m0.slug+"/no-description.md", fm("type", "Topic", "title", "No description", "status", "active")+"\n# No description\n\nMissing a required field. Mentions a site walk, a forbidden term.\n", unborn)
	g.add("data/"+m0.slug+"/bad-status.md", fm("type", "Topic", "title", "Bad status", "description", "Has a status outside the contract.", "status", "wip")+"\n# Bad status\n", unborn)
	g.add("data/"+m0.slug+"/wrong-type.md", fm("type", "Recipe", "title", "Wrong type", "description", "A type the strict Map does not allow.")+"\n# Wrong type\n", unborn)
	g.add("data/"+m1.slug+"/Mixed_Case_Name.md", fm("type", "Note", "title", "Mixed case file name")+"\n# Mixed case\n\nLinks twice to [["+m1.ids[0]+"]] and [["+m1.ids[0]+"]].\n", unborn)
	g.add("data/"+m1.slug+"/untitled.md", fm("type", "Note", "tags", `"[alpha, beta]"`)+"\nNo title, and a list written as a string. Uses {{repo:undeclared-tool}}.\n", unborn)
	if len(g.retired) > 0 {
		g.add("data/"+m1.slug+"/points-at-retired.md", fm("type", "Note", "title", "Points at a retired concept")+"\n# Points at retired\n\nStill links to [["+g.retired[0]+"]].\n", unborn)
	}
	g.add("data/"+m1.slug+"/notes.txt", "a stray file in a Map\n", unborn)
	g.add("data/scratch/loose-note.md", fm("type", "Note", "title", "Loose note")+"\n# Loose note\n\nIn a folder with no _map.md.\n", unborn)
}

// indexes lists each Map's concepts in its index.md, leaving a few out.
func (g *kbgen) indexes(c int) {
	byDir := map[string][]string{}
	titles := map[string]string{}
	for _, f := range g.files {
		p := f.path
		if !strings.HasPrefix(p, "data/") || !strings.HasSuffix(p, ".md") || strings.HasSuffix(p, "/_map.md") || p == "data/index.md" || p == "data/log.md" {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(p, "data/"), ".md")
		id = strings.TrimSuffix(id, "/index")
		dir := id[:strings.Index(id, "/")]
		if dir == "scratch" {
			continue
		}
		byDir[dir] = append(byDir[dir], id)
		titles[id] = id[strings.LastIndex(id, "/")+1:]
	}
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	var root strings.Builder
	root.WriteString(fm("type", "Index", "title", "Test KB "+g.spec.Name) + "\n# Test KB " + g.spec.Name + "\n\nA synthetic knowledge base generated to exercise every view of the Atlas.\n\n")
	for _, d := range dirs {
		fmt.Fprintf(&root, "- [%s](%s/index.md)\n", d, d)
		ids := byDir[d]
		sort.Strings(ids)
		var b strings.Builder
		b.WriteString(fm("type", "Index", "title", d) + "\n# " + d + "\n\n")
		for i, id := range ids {
			if i == 3 && d == g.maps[0].slug {
				continue // index_incomplete
			}
			fmt.Fprintf(&b, "- [[%s]] — %s\n", id, title(titles[id]))
		}
		g.add("data/"+d+"/index.md", b.String(), c)
	}
	g.byPath["data/index.md"].versions[0] = root.String()
}

// artifacts writes skills, agents, hooks and MCP descriptors that read
// concepts, services with secrets, and the placeholder registry.
func (g *kbgen) artifacts(c int) {
	pick := func() string { return g.ids[g.r.n(len(g.ids))] }
	nSkills := max(2, min(8, g.spec.Concepts/400))
	for i := 1; i <= nSkills; i++ {
		ref := pick()
		if i == nSkills {
			ref = g.maps[0].slug + "/no-such-concept" // skill_broken_ref
		}
		g.add(fmt.Sprintf("skills/skill-%d/SKILL.md", i), fmt.Sprintf("---\nname: skill-%d\ndescription: Test skill %d, reads a few concepts first\n---\n# skill-%d\n\nRead first:\n\n- [[%s]]\n- [[%s]]\n", i, i, i, pick(), ref), c)
	}
	g.add("agents/reviewer.md", "---\nname: reviewer\ndescription: Reviews new concepts before they land\n---\nYou review.\n\n- [["+pick()+"]]\n", c)
	g.add("agents/planner.md", "---\nname: planner\ndescription: Plans the open work\n---\nYou plan.\n\n- [[work/task-001]]\n", c)
	g.add("hooks/session-brief/hook.json", `{"event":"SessionStart","command":"run.sh"}`+"\n", c)
	g.add("hooks/session-brief/run.sh", "#!/bin/sh\necho 'Read [["+pick()+"]] before you start.'\n", c)
	g.add("hooks/broken/hook.json", `{"event":"NoSuchEvent"}`+"\n", c)
	g.add("mcp/tickets.json", `{"type":"http","url":"https://tickets.example.com/mcp","headers":{"Authorization":"Bearer ${TICKETS_TOKEN}"}}`+"\n", c)
	g.add("mcp/local-files.json", `{"type":"stdio","command":"files-mcp","args":["--root","/srv/shared"],"env":{"FILES_KEY":"${FILES_KEY}"}}`+"\n", c)

	g.add("paths.yaml", "paths:\n  shared-drive: {description: the team's shared folder, default: "+g.sharedDefault()+"}\n  old-archive: {description: an archive nobody cites any more, default: ~/archive}\nrepos:\n  tooling: {description: the scripts the team runs, remote: https://gitlab.example.com/tools/tooling.git}\n", c)

	g.add(".sops.yaml", "creation_rules:\n  - path_regex: secrets/.*\\.sops\\.yaml$\n    age: age1testtesttesttesttesttesttesttesttesttesttesttesttesttestqqqq\n", c)
	g.add("secrets/services.sops.yaml", sopsFile("tickets:\n    token: "+enc("tok")+"\nstorage:\n    user: "+enc("usr")+"\n    password: "+enc("pwd")+"\n"), c)
	g.add("secrets/env.sops.yaml", sopsFile("FILES_KEY: "+enc("key")+"\n"), c)
	g.add("secrets/broken.sops.yaml", "this: [is not a sops file\n", c)
	g.add("services/tickets.md", fm("type", "Service", "title", "Ticket tracker", "description", "Where the work is tracked.", "kind", "api", "base_url", "https://tickets.example.com/", "status", "active",
		"secret_refs", list("TICKETS_TOKEN=secrets/services.sops.yaml#/tickets/token"))+"\n# Ticket tracker\n\nTasks in [[work/task-001]] are mirrored here.\n", c)
	g.add("services/storage.md", fm("type", "Service", "title", "File storage", "description", "The shared file store.", "kind", "storage", "base_url", "https://files.example.com/", "status", "active",
		"secret_refs", list("STORAGE_USER=secrets/services.sops.yaml#/storage/user", "STORAGE_PASSWORD=secrets/services.sops.yaml#/storage/password"), "secrets_source", "secrets/env.sops.yaml")+"\n# File storage\n", c)
	g.add("services/legacy-wiki.md", fm("type", "Service", "title", "Legacy wiki", "description", "The wiki this KB replaced.", "kind", "web", "base_url", "https://wiki.example.com/", "status", "deprecated")+"\n# Legacy wiki\n", c)
}

// sharedDefault differs between two KBs, so the Paths view shows a key two
// KBs declare with different defaults.
func (g *kbgen) sharedDefault() string {
	if g.spec.Concepts == 3000 {
		return "~/Shared Files"
	}
	return "~/shared"
}

func enc(s string) string {
	return "ENC[AES256_GCM,data:" + s + "dGVzdA==,iv:dGVzdGl2dGVzdGl2dGVzdGl2,tag:dGVzdHRhZ3Rlc3R0YWc=,type:str]"
}

func sopsFile(body string) string {
	return body + "sops:\n    age:\n        - recipient: age1testtesttesttesttesttesttesttesttesttesttesttesttesttestqqqq\n          enc: |\n            -----BEGIN AGE ENCRYPTED FILE-----\n            dGVzdA==\n            -----END AGE ENCRYPTED FILE-----\n    lastmodified: \"2026-01-01T00:00:00Z\"\n    mac: " + enc("mac") + "\n    unencrypted_suffix: _unencrypted\n    version: 3.10.2\n"
}

// history spreads the births over commits and adds revisions, a move, a
// delete, a supersede and repairs; the newest commits fall in the last week.
func (g *kbgen) history() {
	var births []*file
	for _, f := range g.files {
		if f.born == unborn {
			births = append(births, f)
		}
	}
	// Shuffled, so the graph grows everywhere at once.
	for i := len(births) - 1; i > 0; i-- {
		j := g.r.n(i + 1)
		births[i], births[j] = births[j], births[i]
	}
	nBirth := max(10, min(120, len(births)/25))
	chunk := (len(births) + nBirth - 1) / nBirth
	for i := 0; i < len(births); i += chunk {
		end := min(len(births), i+chunk)
		subject := "concept_batch: " + fmt.Sprint(end-i) + " concepts"
		if end-i == 1 {
			subject = "concept_write: " + strings.TrimSuffix(strings.TrimPrefix(births[i].path, "data/"), ".md")
		}
		c := g.commitAt(subject, "filed from the survey notes", 0.02+0.86*float64(i)/float64(len(births)))
		for _, f := range births[i:end] {
			f.born = c
		}
	}
	// Revisions: a share of concepts edited once, a few many times.
	var concepts []*file
	for _, f := range births {
		if strings.HasPrefix(f.path, "data/") && strings.HasSuffix(f.path, ".md") && !strings.Contains(f.path, "/index.md") {
			concepts = append(concepts, f)
		}
	}
	nRev := max(5, len(concepts)/20)
	for i := 0; i < nRev; i++ {
		f := concepts[g.r.n(len(concepts))]
		if f.reason != "" {
			continue
		}
		times := 1
		if i%10 == 0 {
			times = 4
		}
		for t := 0; t < times; t++ {
			id := strings.TrimSuffix(strings.TrimPrefix(f.path, "data/"), ".md")
			c := g.commitAt("concept_patch: "+id, []string{"fixed a wrong value", "added a source", "clarified the definition", "split a long paragraph"}[g.r.n(4)], 0.5+0.49*g.r.f())
			last := f.versions[len(f.versions)-1]
			f.versions = append(f.versions, last+fmt.Sprintf("\nRevision note %d.\n", len(f.versions)))
			f.revs = append(f.revs, c)
		}
	}
	// A move, a delete and a supersede, all in the last week.
	if len(concepts) > 10 {
		mv := concepts[len(concepts)/2]
		if !strings.Contains(mv.path, "island") && !strings.Contains(mv.path, "/field-kit") {
			to := strings.TrimSuffix(mv.path, ".md") + "-renamed.md"
			from := mv.path
			c := g.commitAt("concept_move: "+idOf(from)+" -> "+idOf(to), "a clearer name", 0.985)
			mv.movedFrom, mv.path, mv.moveAt = from, to, c
		}
		del := g.add("data/"+g.maps[0].slug+"/to-be-deleted.md", fm("type", "Note", "title", "To be deleted")+"\n# To be deleted\n", births[0].born)
		del.deleteAt = g.commitAt("concept_delete: "+idOf(del.path), "a duplicate of another concept", 0.99)
	}
	for _, f := range g.files {
		if strings.HasPrefix(f.reason, "supersede: ") {
			f.revs = append(f.revs, g.commitAt(f.reason, "a better concept covers it", 0.9+0.09*g.r.f()))
		}
	}
	// The repairs touch a concept each, so their commits are not empty.
	for _, r := range [][3]string{
		{"auto-repair (background): duplicate_link", "auto-repair: removed a duplicated link", "0.995"},
		{"kb_repair: index_incomplete", "kb_repair: listed missing concepts in the index", "0.997"},
	} {
		start := g.r.n(len(concepts))
		f := concepts[start]
		for k := 1; (f.reason != "" || f.movedFrom != "" || f.deleteAt > 0) && k < len(concepts); k++ {
			f = concepts[(start+k)%len(concepts)]
		}
		var frac float64
		fmt.Sscan(r[2], &frac)
		f.revs = append(f.revs, g.commitAt(r[0], r[1], frac))
		f.versions = append(f.versions, f.versions[len(f.versions)-1]+"\nRepaired.\n")
	}
}

func idOf(path string) string { return strings.TrimSuffix(strings.TrimPrefix(path, "data/"), ".md") }

// writeFinal writes every file at its last version, without git.
func (g *kbgen) writeFinal(root string) error {
	for _, f := range g.files {
		if f.deleteAt > 0 {
			continue
		}
		if err := write(root, f.path, f.versions[len(f.versions)-1]); err != nil {
			return err
		}
	}
	return nil
}

func write(root, rel, content string) error {
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

// replay creates the repository and plays the commits in time order.
func (g *kbgen) replay(root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if err := gitRun(root, nil, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	type op struct {
		f       *file
		version int // -1 move, -2 delete
	}
	ops := make([][]op, len(g.commits))
	for _, f := range g.files {
		ops[f.born] = append(ops[f.born], op{f, 0})
		for i, c := range f.revs {
			ops[c] = append(ops[c], op{f, i + 1})
		}
		if f.movedFrom != "" {
			ops[f.moveAt] = append(ops[f.moveAt], op{f, -1})
		}
		if f.deleteAt > 0 {
			ops[f.deleteAt] = append(ops[f.deleteAt], op{f, -2})
		}
	}
	order := make([]int, len(g.commits))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return g.commits[order[a]].at.Before(g.commits[order[b]].at) })
	// A commit with nothing in it is skipped; time order is kept by
	// clamping each commit to after the previous one.
	var prev time.Time
	for _, ci := range order {
		c := g.commits[ci]
		if len(ops[ci]) == 0 {
			continue
		}
		for _, o := range ops[ci] {
			f := o.f
			switch o.version {
			case -1:
				if err := gitRun(root, nil, "mv", f.movedFrom, f.path); err != nil {
					return err
				}
			case -2:
				if err := gitRun(root, nil, "rm", "-q", f.path); err != nil {
					return err
				}
			default:
				path := f.path
				if f.movedFrom != "" && ci < f.moveAt {
					path = f.movedFrom
				}
				if err := write(root, path, f.versions[o.version]); err != nil {
					return err
				}
			}
		}
		at := c.at
		if !at.After(prev) {
			at = prev.Add(time.Minute)
		}
		prev = at
		msg := c.subject
		if c.reason != "" || c.agent != "" {
			msg += "\n"
			if c.reason != "" {
				msg += "\nReason: " + c.reason
			}
			if c.agent != "" {
				msg += "\nAgent: " + c.agent
			}
		}
		env := []string{
			"GIT_AUTHOR_DATE=" + at.Format(time.RFC3339), "GIT_COMMITTER_DATE=" + at.Format(time.RFC3339),
			"GIT_AUTHOR_NAME=" + c.author, "GIT_AUTHOR_EMAIL=" + c.author + "@example.com",
			"GIT_COMMITTER_NAME=" + c.author, "GIT_COMMITTER_EMAIL=" + c.author + "@example.com",
		}
		if err := gitRun(root, nil, "add", "-A"); err != nil {
			return err
		}
		if err := gitRun(root, env, "commit", "-q", "--no-verify", "--allow-empty", "-m", msg); err != nil {
			return err
		}
	}
	if g.spec.Remote != "" {
		return gitRun(root, nil, "remote", "add", "origin", g.spec.Remote)
	}
	return nil
}

func gitRun(dir string, env []string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// localState writes what the server keeps beside the repository: the lint
// trend, search misses, artifact usage, background repairs and, for one KB,
// git conflicts.
func (g *kbgen) localState(root string) error {
	dir := filepath.Join(root, ".cartographer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var hist, misses, repairs bytes.Buffer
	enc := func(b *bytes.Buffer, v any) {
		raw, _ := json.Marshal(v)
		b.Write(raw)
		b.WriteByte('\n')
	}
	base := 40 + g.spec.Concepts/50
	for d := 89; d >= 1; d-- {
		total := base + d/3 + g.r.n(6)
		enc(&hist, map[string]any{"at": g.day.AddDate(0, 0, -d).Add(6 * time.Hour).Format(time.RFC3339), "total": total,
			"by_severity": map[string]int{"error": total / 10, "warning": total / 3, "info": total - total/10 - total/3},
			"by_handler":  map[string]int{"auto": total / 4, "agent": total / 2, "human": total - total/4 - total/2}})
	}
	for i, q := range []string{"glacier retreat", "kiln temperature", "how to file a source", "budget 2026", "onboarding", "glacier retreat"} {
		enc(&misses, map[string]string{"at": g.day.AddDate(0, 0, -i*3-1).Format(time.RFC3339), "query": q})
	}
	for d := 20; d >= 1; d -= 4 {
		enc(&repairs, map[string]any{"at": g.day.AddDate(0, 0, -d).Format(time.RFC3339), "checks": []map[string]any{{"check": "duplicate_link", "applied": 1 + d%3}, {"check": "index_incomplete", "applied": 0}}})
	}
	usage := map[string]any{"entries": []map[string]any{
		{"name": "skill-1", "kind": "skill", "provider": "claude", "last_used": g.day.AddDate(0, 0, -2).Format(time.RFC3339), "count": 37},
		{"name": "skill-2", "kind": "skill", "provider": "codex", "last_used": g.day.AddDate(0, 0, -60).Format(time.RFC3339), "count": 3},
		{"name": "reviewer", "kind": "agent", "provider": "claude", "last_used": g.day.AddDate(0, 0, -5).Format(time.RFC3339), "count": 12},
	}}
	files := map[string][]byte{"lint-history.jsonl": hist.Bytes(), "search-misses.jsonl": misses.Bytes(), "auto-repair-log.jsonl": repairs.Bytes()}
	raw, _ := json.MarshalIndent(usage, "", "  ")
	files["usage.json"] = raw
	if g.spec.Conflicts {
		id := g.ids[1]
		conflicts := []map[string]any{
			{"concept_id": id, "path": "data/" + id + ".md", "local_sha": strings.Repeat("a", 40), "remote_sha": strings.Repeat("b", 40), "branch": "main", "files": []string{"data/" + id + ".md"}, "detected_at": g.day.Add(-3 * time.Hour).Format(time.RFC3339)},
			{"concept_id": "data/index.md", "path": "data/index.md", "local_sha": strings.Repeat("c", 40), "remote_sha": strings.Repeat("d", 40), "branch": "main", "files": []string{"data/index.md", "data/log.md"}, "detected_at": g.day.Add(-2 * time.Hour).Format(time.RFC3339), "kind": "reserved"},
		}
		raw, _ := json.MarshalIndent(conflicts, "", "  ")
		files["conflicts.json"] = raw
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
