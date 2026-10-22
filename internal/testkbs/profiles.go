package testkbs

// Profile is the shape of one KB, as numbers only: it is the whole of what the
// fleet takes from the real KBs a maintainer measured (D1032). The values below
// are those measurements, anonymised and rounded; no name, title, type,
// vocabulary or text of the measured instance is, or may be, in this package.
// What a profile fixes is how big, how linked and how old a KB is; what it says
// is invented by the theme (themes.go).
type Profile struct {
	// Maps are the concepts of each Map, largest first; Journals are the sizes
	// of the Maps among them that are journals.
	Maps     []int
	Journals []int
	// Types is the largest number of `type`s a single Map holds.
	Types int
	// Expanded concepts, and the satellites each owns (median and largest).
	// Satellites are the pages inside an expanded concept; they come on top of
	// Maps, which count top-level concepts.
	Expanded, SatP50, SatMax int
	// Bytes of a concept file, and the concepts past 90 KB.
	BytesP50, BytesP90, BytesMax int
	Over90                       int
	// Headings (## and ###) of a concept, median.
	Headings int
	// Outbound links of a concept, the concepts with none, and the inbound
	// links of the top hub.
	OutP50, OutP90, OutMax int
	NoOut                  int
	Hub                    int
	// Frontmatter keys of a concept, median and largest.
	FMP50, FMMax int
	// Placeholders cited in bodies, concepts declaring secrets_source, sops files.
	Placeholders, SecretConcepts, SopsFiles int
	// Assets owned by expanded concepts, and what kinds they are.
	Assets     int
	AssetKinds []string
	// Provisioned artifacts and services.
	Skills, Agents, Hooks, Services int
	// History: commits, the span they cover in days and how many fall in the
	// last 30 days.
	Commits, SpanDays, Last30 int
	// Status mix, most frequent first. "none" is no status key, "custom" a
	// vocabulary only the theme's custom Map uses.
	Status []StatusCount
	// Plant adds the findings the Health view is judged on (an island, broken
	// links, the defects of defects()): on one KB of the fleet only, so the
	// other profiles stay clean.
	Plant bool
}

// StatusCount is one entry of a status mix.
type StatusCount struct {
	Status string
	N      int
}

// Concepts is the number of top-level concepts: the sum of the Maps.
func (p *Profile) Concepts() int {
	n := 0
	for _, m := range p.Maps {
		n += m
	}
	return n
}

func sc(pairs ...any) []StatusCount {
	var out []StatusCount
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, StatusCount{pairs[i].(string), pairs[i+1].(int)})
	}
	return out
}

// profiles are P1…P9 of the plan, largest first, keyed by the theme invented
// for each.
var profiles = map[string]*Profile{
	"seed-bank": {
		Maps: []int{490, 120, 110, 63, 30, 27, 22, 11, 10, 7, 3, 3, 2}, Journals: []int{490, 3}, Types: 4,
		BytesP50: 2400, BytesP90: 7000, BytesMax: 35000, Headings: 1,
		OutP50: 1, OutP90: 10, OutMax: 115, NoOut: 13, Hub: 140, FMP50: 7, FMMax: 10,
		Placeholders: 1, Assets: 1, AssetKinds: []string{"text"}, Skills: 1,
		Commits: 2400, SpanDays: 11, Last30: 2400,
		Status: sc("none", 505, "active", 400),
	},
	"ferry-line": {
		Maps: []int{320, 190, 160, 63, 59, 51, 31, 6, 5}, Journals: []int{320, 63, 6}, Types: 5,
		Expanded: 75, SatP50: 3, SatMax: 125,
		BytesP50: 13000, BytesP90: 26000, BytesMax: 115000, Over90: 4, Headings: 14,
		OutP50: 4, OutP90: 11, OutMax: 130, NoOut: 30, Hub: 170, FMP50: 6, FMMax: 12,
		Placeholders: 300, SecretConcepts: 15, SopsFiles: 18,
		Assets: 130, AssetKinds: []string{"script", "csv", "text", "image", "diagram", "office"},
		Skills: 36, Agents: 6,
		Commits: 5600, SpanDays: 54, Last30: 3700,
		Status: sc("active", 425, "reference", 136, "archived", 92, "done", 70, "in-progress", 55, "open", 34,
			"proposed", 22, "custom", 20, "blocked", 19, "none", 9),
		Plant: true,
	},
	"observatory": {
		Maps: []int{64, 47, 32, 32, 30, 19, 18, 16, 15, 7, 2, 2}, Journals: []int{47, 16, 2}, Types: 3,
		Expanded: 10, SatP50: 3, SatMax: 15,
		BytesP50: 4000, BytesP90: 16000, BytesMax: 40000, Headings: 6,
		OutP50: 4, OutP90: 11, OutMax: 40, NoOut: 20, Hub: 40, FMP50: 6, FMMax: 9,
		Placeholders: 200, SopsFiles: 1, Assets: 7, AssetKinds: []string{"script"},
		Skills: 25, Agents: 1, Hooks: 2, Services: 1,
		Commits: 3300, SpanDays: 200, Last30: 1400,
		Status: sc("active", 94, "none", 75, "done", 52, "deprecated", 28, "archived", 16, "open", 14, "draft", 5),
	},
	"library-consortium": {
		Maps: []int{36, 24, 11, 10, 10, 8, 6, 5, 3}, Journals: []int{36}, Types: 3,
		Expanded: 5, SatP50: 1, SatMax: 6,
		BytesP50: 4600, BytesP90: 14000, BytesMax: 48000, Headings: 7,
		OutP50: 5, OutP90: 12, OutMax: 21, NoOut: 10, Hub: 24, FMP50: 5, FMMax: 9,
		Placeholders: 38, SopsFiles: 1, Assets: 7, AssetKinds: []string{"diagram", "deck", "image"},
		Skills: 5, Agents: 4, Services: 1,
		Commits: 920, SpanDays: 40, Last30: 660,
		Status: sc("accepted", 39, "done", 25, "active", 11, "proposed", 11, "none", 9, "reference", 5, "draft", 5,
			"superseded", 2, "open", 2, "in-progress", 2, "rejected", 2),
	},
	"mountain-rescue": {
		Maps: []int{17, 11, 9, 9, 8, 6, 6, 4, 4, 3}, Journals: []int{17}, Types: 6,
		Expanded: 2, SatP50: 3, SatMax: 3,
		BytesP50: 5500, BytesP90: 13000, BytesMax: 49000, Headings: 6,
		OutP50: 3, OutP90: 11, OutMax: 18, NoOut: 12, Hub: 27, FMP50: 6, FMMax: 10,
		Placeholders: 14, SecretConcepts: 1, SopsFiles: 1,
		Commits: 520, SpanDays: 89, Last30: 345,
		Status: sc("active", 28, "none", 18, "archived", 9, "draft", 6, "accepted", 5, "completed", 5,
			"superseded", 2, "reference", 2, "custom", 2),
	},
	"vineyard-coop": {
		Maps: []int{15, 13, 10, 7, 7, 6, 4, 3, 3, 2, 2}, Journals: []int{4, 3, 2, 2}, Types: 6,
		BytesP50: 1400, BytesP90: 2600, BytesMax: 5700, Headings: 1,
		OutP50: 2, OutP90: 5, OutMax: 15, NoOut: 12, Hub: 14, FMP50: 6, FMMax: 8,
		Skills:  5,
		Commits: 120, SpanDays: 15, Last30: 120,
		Status: sc("active", 49, "none", 12, "draft", 5, "open", 5),
	},
	"choir-season": {
		Maps: []int{34, 12, 3, 2, 2}, Journals: []int{34}, Types: 1,
		Expanded: 2,
		BytesP50: 2000, BytesP90: 4700, BytesMax: 14000, Headings: 6,
		OutP50: 2, OutP90: 3, OutMax: 16, NoOut: 16, Hub: 26, FMP50: 11, FMMax: 11,
		Placeholders: 1, Assets: 5, AssetKinds: []string{"script", "spreadsheet", "pdf"}, Skills: 2,
		Commits: 590, SpanDays: 90, Last30: 290,
		Status: sc("custom", 37, "none", 16),
	},
	"museum-restoration": {
		Maps: []int{12, 10, 9, 5}, Journals: []int{10, 5}, Types: 5,
		Expanded: 5,
		BytesP50: 2400, BytesP90: 9000, BytesMax: 17000, Headings: 4,
		OutP50: 3, OutP90: 11, OutMax: 23, NoOut: 8, Hub: 24, FMP50: 6, FMMax: 8,
		Placeholders: 5, Assets: 7, AssetKinds: []string{"office", "pdf"},
		Commits: 130, SpanDays: 8, Last30: 130,
		Status: sc("none", 13, "draft", 10, "accepted", 6, "open", 2, "active", 2),
	},
	"bike-share-pilot": {
		Maps: []int{7, 4, 3, 2}, Journals: []int{3, 2}, Types: 2,
		BytesP50: 1000, BytesP90: 3900, BytesMax: 4100, Headings: 4,
		OutP50: 1, OutP90: 5, OutMax: 10, NoOut: 5, Hub: 10, FMP50: 6, FMMax: 7,
		Assets: 1, AssetKinds: []string{"csv"},
		Commits: 41, SpanDays: 1, Last30: 41,
		Status: sc("draft", 8, "none", 5),
	},
}
