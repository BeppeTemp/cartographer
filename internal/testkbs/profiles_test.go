package testkbs

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

var (
	genDir  string
	genMu   sync.Mutex
	genOnce = map[string]*sync.Once{}
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "testkbs-")
	if err != nil {
		panic(err)
	}
	genDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// generated builds the named profiled KB once for the whole test run.
func generated(t *testing.T, name string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	genMu.Lock()
	once := genOnce[name]
	if once == nil {
		once = new(sync.Once)
		genOnce[name] = once
	}
	genMu.Unlock()
	root := filepath.Join(genDir, name)
	var err error
	once.Do(func() {
		for _, s := range Fleet() {
			if s.Name == name {
				err = Generate(root, s, day)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, serr := os.Stat(root); serr != nil {
		t.Fatalf("%s was not generated: %v", name, serr)
	}
	return root
}

// shape is what a generated KB measures, read back through the kb package.
type shape struct {
	perMap      map[string]int // top-level concepts per Map
	top         int
	sats        map[string]int // satellites per expanded concept
	bytes       []int
	status      map[string]int // "" is no status key
	keys        []int
	titles      []string
	hubIn       int
	noOut       int
	placeholder int
	secrets     int
	assets      int
}

func measure(t *testing.T, root string, th *theme) shape {
	t.Helper()
	k, err := kb.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	links, err := k.Links()
	if err != nil {
		t.Fatal(err)
	}
	slugs := map[string]bool{}
	for _, m := range th.maps {
		slugs[m.slug] = true
	}
	sh := shape{perMap: map[string]int{}, sats: map[string]int{}, status: map[string]int{}}
	err = k.WalkConceptPaths(func(id okf.ConceptID, phys, content string) error {
		parts := strings.Split(string(id), "/")
		if !slugs[parts[0]] {
			return nil
		}
		raw, _, _ := okf.SplitFrontmatter(content)
		fm, err := okf.ParseFrontmatter(raw)
		if err != nil {
			t.Errorf("%s: %v", id, err)
			return nil
		}
		switch {
		case len(parts) == 3:
			sh.sats[parts[0]+"/"+parts[1]]++
			return nil
		case len(parts) != 2:
			return nil
		}
		sh.perMap[parts[0]]++
		sh.top++
		sh.bytes = append(sh.bytes, len(content))
		st, _ := fm.Get("status")
		status, _ := st.(string)
		sh.status[status]++
		sh.keys = append(sh.keys, strings.Count(raw, "\n")+1)
		ti, _ := fm.Get("title")
		title, _ := ti.(string)
		sh.titles = append(sh.titles, title)
		if n := len(links.In[id]); n > sh.hubIn {
			sh.hubIn = n
		}
		if len(links.Out[id]) == 0 {
			sh.noOut++
		}
		sh.placeholder += strings.Count(content, "{{repo:") + strings.Count(content, "{{path:")
		if _, ok := fm.Get("secrets_source"); ok {
			sh.secrets++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	filepath.WalkDir(filepath.Join(root, "data"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.HasSuffix(p, ".md") && !isDefect(p) {
			sh.assets++
		}
		return nil
	})
	return sh
}

func within(got, want int, pct float64, slack int) bool {
	d := float64(want) * pct
	return float64(got) >= float64(want)-d-float64(slack) && float64(got) <= float64(want)+d+float64(slack)
}

func percentile(xs []int, p float64) int {
	s := append([]int(nil), xs...)
	sort.Ints(s)
	return s[int(float64(len(s)-1)*p)]
}

func countFiles(t *testing.T, root, pattern string) int {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(root, pattern))
	if err != nil {
		t.Fatal(err)
	}
	return len(m)
}

// Every theme lines up with its profile: one themed Map per profiled Map, the
// journals are journal-shaped, no Map has more types than the profile allows
// and one has exactly that many.
func TestThemesLineUpWithProfiles(t *testing.T) {
	for _, name := range profiled {
		p, th := profiles[name], themes[name]
		if th == nil || len(th.maps) != len(p.Maps) {
			t.Fatalf("%s: the theme has %d Maps, the profile %d", name, len(th.maps), len(p.Maps))
		}
		journal := make([]bool, len(p.Maps))
		for _, js := range p.Journals {
			found := false
			for i, s := range p.Maps {
				if s == js && !journal[i] {
					journal[i], found = true, true
					break
				}
			}
			if !found {
				t.Errorf("%s: no Map for the journal of %d", name, js)
			}
		}
		most := 0
		for i, m := range th.maps {
			isJournalRole := strings.IndexByte("LSQ", m.role) >= 0
			if isJournalRole != journal[i] {
				t.Errorf("%s: Map %s is role %c but journal=%v", name, m.slug, m.role, journal[i])
			}
			most = max(most, len(m.types))
		}
		if most != p.Types {
			t.Errorf("%s: the largest Map has %d types, the profile says %d", name, most, p.Types)
		}
		if p.SecretConcepts > 0 {
			room := false
			for i, m := range th.maps {
				room = room || (m.role == roleTopic && !journal[i] && len(m.types) < p.Types)
			}
			if !room {
				t.Errorf("%s: no Map has room for the Service type of its secrets_source concepts", name)
			}
		}
		if th.customMap == "" && statusCount(p, "custom") > 0 {
			t.Errorf("%s: custom statuses and no custom Map", name)
		}
		if p.Concepts() < 10 {
			t.Errorf("%s: %d concepts", name, p.Concepts())
		}
	}
}

func statusCount(p *Profile, s string) int {
	for _, m := range p.Status {
		if m.Status == s {
			return m.N
		}
	}
	return 0
}

// A generated KB takes the shape of its profile: concepts and Maps, journals,
// expanded concepts, sizes, the hub, the status mix, assets, artifacts and
// history, each within 5% or exact where the profile is exact (D1032).
func TestProfiledKBsFollowTheirProfile(t *testing.T) {
	for _, name := range profiled {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := generated(t, name)
			p, th := profiles[name], themes[name]
			sh := measure(t, root, th)
			slack := 0
			if p.Plant {
				slack = 10 // the planted defects add a few pages
			}
			if !within(sh.top, p.Concepts(), 0.05, slack) {
				t.Errorf("%d concepts, the profile has %d", sh.top, p.Concepts())
			}
			for i, size := range p.Maps {
				m := th.maps[i]
				if !within(sh.perMap[m.slug], size, 0.05, 0) && !(p.Plant && within(sh.perMap[m.slug], size, 0.05, 4)) {
					t.Errorf("Map %s has %d concepts, the profile has %d", m.slug, sh.perMap[m.slug], size)
				}
				data, err := os.ReadFile(filepath.Join(root, "data", m.slug, "_map.md"))
				if err != nil {
					t.Fatal(err)
				}
				if isJournal := strings.Contains(string(data), "kind: journal"); isJournal != (strings.IndexByte("LSQ", m.role) >= 0) {
					t.Errorf("Map %s: journal=%v for role %c", m.slug, isJournal, m.role)
				}
			}
			// Expanded concepts and their satellites.
			wantExp := p.Expanded
			if wantExp == 0 && p.Assets > 0 {
				wantExp = 1 // an asset needs an expanded concept to live in
			}
			expanded, maxSats := countExpanded(t, root, th), 0
			for _, n := range sh.sats {
				maxSats = max(maxSats, n)
			}
			if expanded != wantExp {
				t.Errorf("%d expanded concepts, the profile has %d", expanded, wantExp)
			}
			if maxSats != p.SatMax {
				t.Errorf("most satellites %d, the profile has %d", maxSats, p.SatMax)
			}
			// Sizes.
			over := 0
			for _, b := range sh.bytes {
				if b > 90*1024 {
					over++
				}
			}
			if over != p.Over90 {
				t.Errorf("%d concepts past 90 KB, the profile has %d", over, p.Over90)
			}
			if got := percentile(sh.bytes, 1); !within(got, p.BytesMax, 0.05, 300) {
				t.Errorf("largest concept %d bytes, the profile has %d", got, p.BytesMax)
			}
			if got := percentile(sh.bytes, 0.5); !within(got, p.BytesP50, 0.35, 700) { // the frontmatter and links are a floor
				t.Errorf("median concept %d bytes, the profile has %d", got, p.BytesP50)
			}
			// Links.
			if !within(sh.hubIn, p.Hub, 0.05, 2) {
				t.Errorf("top hub has %d inbound links, the profile has %d", sh.hubIn, p.Hub)
			}
			if !within(sh.noOut, p.NoOut, 0.05, 3) {
				t.Errorf("%d concepts without a link out, the profile has %d", sh.noOut, p.NoOut)
			}
			// Frontmatter.
			if got := percentile(sh.keys, 0.5); got < p.FMP50-1 || got > p.FMP50+1 {
				t.Errorf("median %d frontmatter keys, the profile has %d", got, p.FMP50)
			}
			if got := percentile(sh.keys, 1); got < p.FMMax || got > p.FMMax+3 {
				t.Errorf("most frontmatter keys %d, the profile has %d", got, p.FMMax)
			}
			// Status mix, scaled to the concepts and followed exactly.
			want := apportion(p.Status, sh.top)
			got := map[string]int{}
			for s, n := range sh.status {
				if s == "" {
					s = "none"
				}
				for _, c := range th.custom {
					if s == c && statusCount(p, "custom") > 0 {
						s = "custom"
					}
				}
				got[s] += n
			}
			for i, m := range p.Status {
				if !within(got[m.Status], want[i], 0, 0) && !(p.Plant && within(got[m.Status], want[i], 0, 6)) {
					t.Errorf("status %s on %d concepts, the mix says %d", m.Status, got[m.Status], want[i])
				}
			}
			// Assets, placeholders, secrets and artifacts.
			if sh.assets != p.Assets {
				t.Errorf("%d assets, the profile has %d", sh.assets, p.Assets)
			}
			if !within(sh.placeholder, p.Placeholders, 0.02, 2) {
				t.Errorf("%d placeholders, the profile has %d", sh.placeholder, p.Placeholders)
			}
			if sh.secrets != p.SecretConcepts {
				t.Errorf("%d concepts declare secrets_source, the profile has %d", sh.secrets, p.SecretConcepts)
			}
			if n := countFiles(t, root, "secrets/*.sops.yaml"); n != p.SopsFiles {
				t.Errorf("%d sops files, the profile has %d", n, p.SopsFiles)
			}
			hooks := p.Hooks
			if p.Plant {
				hooks++ // the planted invalid hook
			}
			for _, c := range []struct {
				pattern string
				want    int
			}{{"skills/*/SKILL.md", p.Skills}, {"agents/*.md", p.Agents}, {"hooks/*/hook.json", hooks}, {"services/*.md", p.Services}} {
				if n := countFiles(t, root, c.pattern); n != c.want {
					t.Errorf("%s: %d, the profile has %d", c.pattern, n, c.want)
				}
			}
			// History.
			subjects, first, recent := history(t, root)
			if n := len(subjects); !within(n, p.Commits, 0.1, 0) {
				t.Errorf("%d commits, the profile has %d", n, p.Commits)
			}
			if d := day.Sub(first).Hours() / 24; d < float64(p.SpanDays)-1 || d > float64(p.SpanDays)+1 {
				t.Errorf("the history spans %.1f days, the profile %d", d, p.SpanDays)
			}
			if !within(recent, p.Last30, 0.05, 1) {
				t.Errorf("%d commits in the last 30 days, the profile has %d", recent, p.Last30)
			}
			if p.Commits >= 500 {
				// Most revisions fall on a hot set of about one concept in ten.
				perID := map[string]int{}
				patches := 0
				for _, s := range subjects {
					if id, ok := strings.CutPrefix(s, "concept_patch: "); ok {
						perID[id]++
						patches++
					}
				}
				var counts []int
				for _, n := range perID {
					counts = append(counts, n)
				}
				sort.Sort(sort.Reverse(sort.IntSlice(counts)))
				top := 0
				for i := 0; i < len(counts) && i < sh.top/8; i++ {
					top += counts[i]
				}
				if patches == 0 || top*100/patches < 55 {
					t.Errorf("the top eighth of the concepts holds %d of %d patches", top, patches)
				}
			}
			// About one title in ten carries a letter outside ASCII.
			if sh.top >= 50 {
				non := 0
				for _, ti := range sh.titles {
					for _, r := range ti {
						if r > unicode.MaxASCII {
							non++
							break
						}
					}
				}
				if share := float64(non) / float64(len(sh.titles)); share < 0.07 || share > 0.13 {
					t.Errorf("%.1f%% of the titles are non-ASCII", share*100)
				}
			}
		})
	}
}

func countExpanded(t *testing.T, root string, th *theme) int {
	t.Helper()
	k, err := kb.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	slugs := map[string]bool{}
	for _, m := range th.maps {
		slugs[m.slug] = true
	}
	n := 0
	k.WalkConceptPaths(func(id okf.ConceptID, phys, content string) error {
		if parts := strings.Split(string(id), "/"); len(parts) == 2 && slugs[parts[0]] && strings.HasSuffix(phys, "/index.md") {
			n++
		}
		return nil
	})
	return n
}

// A profiled KB that does not plant findings opens clean: lint reports only
// what the mix itself puts there (a journal whose pages carry a status that
// reads as valid, not as open work), never a structural defect.
func TestProfiledKBsOpenClean(t *testing.T) {
	for _, name := range profiled {
		if profiles[name].Plant {
			continue
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			k, err := kb.Open(generated(t, name))
			if err != nil {
				t.Fatal(err)
			}
			findings, err := lint.Run(k, "", false)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range findings {
				if f.Check != "status_semantics" {
					t.Errorf("%s: %s: %s", f.Check, f.Path, f.Message)
				}
			}
		})
	}
}

// Everything a profiled KB says comes from the vocabulary tables of this
// package and a fixed list of English filler: a real name pasted into a
// fixture, or a leak into a generated page, breaks the build instead of
// reaching a public repository (D259, D1032).
func TestProfiledKBsUseOnlyTheirOwnVocabulary(t *testing.T) {
	allowed := map[string]bool{}
	add := func(texts ...string) {
		for _, tx := range texts {
			for _, w := range tokens(tx) {
				allowed[w] = true
			}
		}
	}
	// What the generator writes around the vocabulary: frontmatter keys and
	// values, the fixed sentences of roles, the placeholders and the URLs.
	add(`type title status timestamp description provenance see also links parts attachments part source report
		question check task definition of done collect entries fix broken ones re read tell readers everything
		superseded by secrets kind locator ingest ingested pending skipped involves contradiction resolution reason
		the two values do not overlap priority waiting on an answer from review after open resolved measurement
		definition missing context secret refs base url api active service a described in that team calls skill for
		reads few concepts first agent you review once pointed at it before start run sh echo session brief
		https example com reports md path repo name txt csv png drawio pptx docx xlsx pdf sh x p store sops yaml such concept services where work tracked tickets measurement scheduled in progress blocked proposed accepted rejected completed
		reference archived deprecated draft done when is to and with at or not no it has had was were be this which
		as uses use used map concepts about shared folder tooling lives scripts team runs token user password`)
	add(fillerPeriods...)
	for _, w := range fillerPeriods {
		add(w + "s") // "after two {period}s of trial use"
	}
	add(fillerRoles...)
	add(fillerSents...)
	add(fillerHeadings...)
	add(adrHeadings...)
	add(logHeadings...)
	add(extraKeys...)
	add(fillerLevels...)
	add(fillerCycles...)
	add(diacritics...)
	add(assetStems...)
	add(fillerTable...)
	add(fillerCode...)
	for _, th := range themes {
		add(th.name, th.customMap)
		add(th.nouns...)
		add(th.adjs...)
		add(th.custom...)
		add(th.paths...)
		add(th.repos...)
		for _, m := range th.maps {
			add(m.slug, m.title)
			add(m.types...)
		}
	}
	for _, s := range sourceKinds {
		add(s)
	}
	var offenders []string
	seen := map[string]bool{}
	for _, name := range profiled {
		root := generated(t, name)
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if d.Name() == ".git" || strings.Contains(p, "/.git/") || strings.Contains(p, "/.cartographer/") || isDefect(p) {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			// The scaffold files and the sops stand-ins are not themed text.
			switch {
			case !strings.HasSuffix(p, ".md") || d.Name() == "_map.md":
				return nil
			case d.Name() == "index.md" && strings.Count(rel, "/") == 2: // a Map's index lists "-renamed" ids
				return nil
			case rel == "instructions.md" || strings.HasPrefix(rel, "templates/") || rel == "data/log.md" || rel == "data/index.md":
				return nil
			}
			data, _ := os.ReadFile(p)
			for _, w := range tokens(string(data)) {
				if !allowed[w] && !seen[w] {
					seen[w] = true
					offenders = append(offenders, name+": "+rel+": "+w)
				}
			}
			return nil
		})
	}
	if len(offenders) > 0 {
		if len(offenders) > 20 {
			offenders = offenders[:20]
		}
		t.Errorf("words outside the vocabulary tables:\n%s", strings.Join(offenders, "\n"))
	}
}

// tokens are the lower-case runs of letters of a text, with their accents.
func tokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) })
}

// The same spec and day give the same KB, history included: the hashes of two
// generations agree (the D818 invariant).
func TestProfiledKBIsDeterministic(t *testing.T) {
	root := generated(t, "museum-restoration")
	other := filepath.Join(t.TempDir(), "museum-restoration")
	for _, s := range Fleet() {
		if s.Name == "museum-restoration" {
			if err := Generate(other, s, day); err != nil {
				t.Fatal(err)
			}
		}
	}
	head := func(r string) string {
		out, err := exec.Command("git", "-C", r, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	if a, b := head(root), head(other); a != b {
		t.Errorf("two generations differ: %s and %s", a, b)
	}
}
