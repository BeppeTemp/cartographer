package testkbs

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
)

// pnode is one top-level concept of a profiled KB.
type pnode struct {
	id    string
	mi    int // index of its Map
	role  byte
	title string
	typ   string
	// status "" is no status key.
	status string
	date   int // days ago
	size   int // target bytes of the file
	keys   int // target frontmatter keys
	// sats are the pages inside it when it is expanded; assets the files it owns.
	sats   []*psat
	assets []passet
	expand bool
	// out are the top-level concepts it links to.
	out        []int
	noOut      bool
	island     bool
	secret     string // sops file its secrets_source names
	supersedes string // concept that replaced it
	phs        int    // placeholders its body cites
	broken     string // a planted broken link
	ingest     string // ingest_status of a source
	svc        []string
	cites      []string
}

type psat struct {
	id   string
	size int
}

type passet struct{ name, content string }

type planner struct {
	g       *kbgen
	p       *Profile
	th      *theme
	r       *rng
	nodes   []*pnode
	byMap   [][]int
	journal []bool
	hub     int
	used    map[string]bool
	inDeg   []int
	cited   map[string]bool // placeholder keys the bodies cite
	types   [][]string      // the types of each Map (a copy: one may gain Service)
}

func (r *rng) norm() float64 {
	u1 := r.f()
	if u1 < 1e-9 {
		u1 = 1e-9
	}
	z := math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*r.f())
	return math.Max(-3, math.Min(3, z))
}

// logn draws from a log-normal with the given median and 90th percentile.
func (r *rng) logn(p50, p90 float64) float64 {
	sigma := 0.3
	if p90 > p50 && p50 > 0 {
		sigma = math.Log(p90/p50) / 1.2816
	}
	return p50 * math.Exp(sigma*r.norm())
}

func (r *rng) pick(xs []string) string { return xs[r.n(len(xs))] }

// apportion splits total by the weights of mix, by largest remainder, so the
// mix is followed exactly.
func apportion(mix []StatusCount, total int) []int {
	sum := 0
	for _, m := range mix {
		sum += m.N
	}
	out := make([]int, len(mix))
	type rem struct {
		i int
		f float64
	}
	var rems []rem
	left := total
	for i, m := range mix {
		exact := float64(m.N) * float64(total) / float64(sum)
		out[i] = int(exact)
		left -= out[i]
		rems = append(rems, rem{i, exact - float64(out[i])})
	}
	sort.SliceStable(rems, func(a, b int) bool { return rems[a].f > rems[b].f })
	for k := 0; left > 0; k, left = (k+1)%len(rems), left-1 {
		out[rems[k].i]++
	}
	return out
}

// planProfile lays out every file and every commit of a profiled KB.
func (g *kbgen) planProfile() {
	p := g.spec.Profile
	th := themes[g.spec.Theme]
	scaffold := g.commitAt("Scaffold the knowledge base", "", 0)
	pl := &planner{g: g, p: p, th: th, r: g.r, used: map[string]bool{}, cited: map[string]bool{}}
	pl.createNodes()
	pl.expand()
	pl.statuses()
	pl.links()
	pl.sizes()
	g.referenced = map[string]bool{}
	for i, n := range pl.nodes {
		for _, j := range n.out {
			g.referenced[pl.nodes[j].id] = true
		}
		g.referenced[n.supersedes] = true
		for _, c := range n.cites {
			g.referenced[c] = true
		}
		if n.expand || i == pl.hub {
			g.referenced[n.id] = true
		}
	}
	// A service is linked from a concept, so it is not an orphan.
	for i := 0; i < p.Services; i++ {
		n := pl.nodes[pl.r.n(len(pl.nodes))]
		n.svc = append(n.svc, "services/"+pl.name(i+300))
	}
	g.scaffoldRoot(scaffold)
	pl.emit()
	g.indexes(scaffold)
	pl.artifacts(scaffold)
	g.historyProfile()
}

func (pl *planner) sentence() string {
	s := fillerSents[pl.r.n(len(fillerSents))]
	for strings.Contains(s, "{") {
		s = strings.Replace(s, "{n}", pl.r.pick(pl.th.nouns), 1)
		s = strings.Replace(s, "{n2}", pl.r.pick(pl.th.nouns), 1)
		s = strings.Replace(s, "{a}", pl.r.pick(pl.th.adjs), 1)
		s = strings.Replace(s, "{period}", pl.r.pick(fillerPeriods), 1)
		s = strings.Replace(s, "{role}", pl.r.pick(fillerRoles), 1)
	}
	return s
}

func (pl *planner) paragraph(minBytes int) string {
	var b strings.Builder
	for b.Len() < minBytes-40 || b.Len() == 0 {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(pl.sentence())
	}
	return b.String()
}

func (pl *planner) unique(base string) string {
	name := base
	for k := 2; pl.used[name]; k++ {
		name = fmt.Sprintf("%s-%d", base, k)
	}
	pl.used[name] = true
	return name
}

func reservedStem(s string) bool { return s == "index" || s == "log" || s == "agents" }

// createNodes names every top-level concept: ids, titles and types by the role
// of its Map.
func (pl *planner) createNodes() {
	p, th := pl.p, pl.th
	pl.journal = make([]bool, len(p.Maps))
	for _, js := range p.Journals {
		for i, s := range p.Maps {
			if s == js && !pl.journal[i] {
				pl.journal[i] = true
				break
			}
		}
	}
	pl.byMap = make([][]int, len(p.Maps))
	pl.types = make([][]string, len(p.Maps))
	secretMi := -1
	for mi := range p.Maps {
		pl.types[mi] = append([]string(nil), th.maps[mi].types...)
		// The concepts that declare secrets_source are Services; the first Map
		// with room for one more type hosts them.
		if secretMi < 0 && p.SecretConcepts > 0 && th.maps[mi].role == roleTopic && !pl.journal[mi] && len(pl.types[mi]) < p.Types {
			secretMi = mi
			pl.types[mi] = append(pl.types[mi], "Service")
		}
	}
	for mi, size := range p.Maps {
		tmap := th.maps[mi]
		tmap.types = pl.types[mi]
		mi2 := mapInfo{slug: tmap.slug, title: tmap.title}
		for k := 0; k < size; k++ {
			n := &pnode{mi: mi, role: tmap.role, date: pl.r.n(170)}
			adj, noun := pl.r.pick(th.adjs), pl.r.pick(th.nouns)
			var name, title string
			switch tmap.role {
			case roleLog:
				n.date = (size - k) * 170 / size
				name = pl.unique(tmap.slug + "/" + fmt.Sprintf("%s-%s", pl.g.date(n.date), noun))
				title = fmt.Sprintf("%s check %s", titleCase(noun), pl.g.date(n.date))
			case roleRef:
				stem := noun
				if reservedStem(stem) {
					stem = adj + "-" + noun
				}
				name = pl.unique(tmap.slug + "/" + stem)
				title = titleCase(strings.ReplaceAll(stem, "-", " "))
			case roleADR:
				name = pl.unique(tmap.slug + "/" + fmt.Sprintf("%04d-%s-%s", k+1, adj, noun))
				title = titleCase(adj + " " + noun)
			case roleWork:
				name = pl.unique(tmap.slug + "/" + fmt.Sprintf("task-%03d", k+1))
				title = fmt.Sprintf("Task %d %s %s", k+1, adj, noun)
			case roleSource:
				n.ingest = []string{"ingested", "ingested", "pending", "skipped"}[k%4]
				name = pl.unique(tmap.slug + "/" + fmt.Sprintf("source-%d", k+1))
				title = fmt.Sprintf("Source report %d %s", k+1, noun)
			case roleQuestion:
				name = pl.unique(tmap.slug + "/" + fmt.Sprintf("question-%d", k+1))
				title = fmt.Sprintf("Question %d %s", k+1, noun)
			default:
				name = pl.unique(tmap.slug + "/" + adj + "-" + noun)
				title = titleCase(adj + " " + noun)
			}
			if len(pl.nodes)%10 == 3 {
				title += " " + diacritics[(len(pl.nodes)/10)%len(diacritics)]
			}
			n.id, n.title = name, title
			// Types: each one at least once in a Map, then at random.
			switch {
			case mi == secretMi && k < p.SecretConcepts:
				n.typ = "Service"
				n.secret = fmt.Sprintf("secrets/%s.sops.yaml", sopsName(k%p.SopsFiles))
			case tmap.role == roleWork && len(tmap.types) > 1:
				n.typ = tmap.types[0]
				if pl.r.chance(0.15) {
					n.typ = tmap.types[1]
				}
			case k < len(tmap.types):
				n.typ = tmap.types[k]
			default:
				n.typ = tmap.types[pl.r.n(len(tmap.types))]
			}
			pl.byMap[mi] = append(pl.byMap[mi], len(pl.nodes))
			pl.nodes = append(pl.nodes, n)
			mi2.ids = append(mi2.ids, name)
		}
		pl.g.maps = append(pl.g.maps, mi2)
		pl.g.ids = append(pl.g.ids, mi2.ids...)
	}
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// expand makes the profile's expanded concepts, with their satellites and
// assets; the one with the most satellites is the hub.
func (pl *planner) expand() {
	p := pl.p
	var hosts []int
	for i, n := range pl.nodes {
		if (n.role == roleTopic || n.role == roleADR) && !pl.journal[n.mi] {
			hosts = append(hosts, i)
		}
	}
	e := p.Expanded
	if e == 0 && p.Assets > 0 {
		e = 1 // an asset needs an expanded concept to live in
	}
	for i := len(hosts) - 1; i > 0; i-- {
		j := pl.r.n(i + 1)
		hosts[i], hosts[j] = hosts[j], hosts[i]
	}
	if e > len(hosts) {
		e = len(hosts)
	}
	hosts = hosts[:e]
	pl.hub = -1
	for hi, h := range hosts {
		n := pl.nodes[h]
		n.expand = true
		if hi == 0 {
			pl.hub = h
		}
		sats := 0
		if p.SatMax > 0 {
			sats = int(math.Round(pl.r.logn(math.Max(float64(p.SatP50), 0.5), math.Max(float64(p.SatP50)*2.5, 1))))
			sats = max(0, min(sats, p.SatMax))
			if hi == 0 {
				sats = p.SatMax
			}
		}
		for j := 1; j <= sats; j++ {
			n.sats = append(n.sats, &psat{id: fmt.Sprintf("%s/%s-%d", n.id, pl.r.pick(pl.th.nouns), j)})
		}
	}
	if pl.hub < 0 {
		for i, n := range pl.nodes {
			if n.role == roleTopic && !pl.journal[n.mi] {
				pl.hub = i
				break
			}
		}
		if pl.hub < 0 {
			pl.hub = 0
		}
	}
	// Assets, spread over the expanded concepts.
	if p.Assets > 0 && len(hosts) > 0 {
		for k := 0; k < p.Assets; k++ {
			n := pl.nodes[hosts[pl.r.n(len(hosts))]]
			kind := p.AssetKinds[k%len(p.AssetKinds)]
			name, content := pl.assetFile(kind, k)
			n.assets = append(n.assets, passet{name, content})
		}
	}
}

func (pl *planner) assetFile(kind string, k int) (string, string) {
	stem := fmt.Sprintf("%s-%d", assetStems[k%len(assetStems)], k+1)
	switch kind {
	case "script":
		return stem + ".sh", "#!/bin/sh\necho " + pl.r.pick(pl.th.nouns) + "\n"
	case "csv":
		return stem + ".csv", "item,value\n" + pl.r.pick(pl.th.nouns) + ",1\n" + pl.r.pick(pl.th.nouns) + ",2\n"
	case "image":
		return stem + ".png", pngBytes
	case "diagram":
		return stem + ".drawio", drawioXML
	case "deck":
		return stem + ".pptx", zipBytes
	case "pdf":
		return stem + ".pdf", pdfBytes
	case "spreadsheet":
		return stem + ".xlsx", zipBytes
	case "office":
		return stem + []string{".docx", ".xlsx", ".pptx"}[k%3], zipBytes
	}
	return stem + ".txt", pl.sentence() + "\n"
}

// Fixed small blobs with the right extension: a 1x1 PNG, a draw.io page, an
// OOXML-shaped zip header, a one-page PDF.
const (
	pngBytes  = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\xff\xff?\x00\x05\xfe\x02\xfe\xa7\x9a\xa0\xa0\x00\x00\x00\x00IEND\xaeB`\x82"
	drawioXML = `<mxfile><diagram name="page"><mxGraphModel><root><mxCell id="0"/><mxCell id="1" parent="0"/></root></mxGraphModel></diagram></mxfile>` + "\n"
	zipBytes  = "PK\x03\x04\x14\x00\x00\x00\x00\x00\x00\x00!\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00PK\x05\x06\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"
	pdfBytes  = "%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 72 72]>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n"
)

// links draws every concept's outbound links: the counts from the profile's
// distribution, the no-link concepts and the hub exact, the rest at random
// with the in-degree of any non-hub concept capped.
func (pl *planner) links() {
	p := pl.p
	N := len(pl.nodes)
	pl.inDeg = make([]int, N)
	hub := pl.nodes[pl.hub]
	// Planted island: a ring of six in the first topical Map, linked only to itself.
	var ring []int
	if p.Plant {
		for _, i := range pl.byMap[1] {
			if i != pl.hub && !pl.nodes[i].expand && len(ring) < 6 {
				ring = append(ring, i)
			}
		}
		for j, i := range ring {
			pl.nodes[i].island = true
			pl.nodes[i].out = []int{ring[(j+1)%len(ring)]}
			pl.inDeg[ring[(j+1)%len(ring)]]++
		}
	}
	// Who has no outbound link: never the hub, an expanded concept or the ring.
	var elig []int
	for i, n := range pl.nodes {
		if i != pl.hub && !n.expand && !n.island {
			elig = append(elig, i)
		}
	}
	pl.shuffle(elig)
	for k := 0; k < p.NoOut && k < len(elig); k++ {
		pl.nodes[elig[k]].noOut = true
	}
	// Out-degree of everyone else.
	deg := make([]int, N)
	for i, n := range pl.nodes {
		if n.noOut || n.island {
			continue
		}
		k := int(math.Round(pl.r.logn(float64(p.OutP50), float64(p.OutP90))))
		k = max(1, min(k, p.OutMax-1))
		if n.role == roleQuestion {
			k = max(k, 2)
		}
		deg[i] = k
	}
	deg[pl.hub] = p.OutMax
	hubOthers := max(0, p.Hub-len(hub.sats))
	// The hub's other inbound links come from concepts that link out anyway.
	var srcs []int
	for i := range pl.nodes {
		if i != pl.hub && !pl.nodes[i].noOut && !pl.nodes[i].island {
			srcs = append(srcs, i)
		}
	}
	pl.shuffle(srcs)
	if hubOthers > len(srcs) {
		hubOthers = len(srcs)
	}
	for _, i := range srcs[:hubOthers] {
		pl.nodes[i].out = append(pl.nodes[i].out, pl.hub)
		pl.inDeg[pl.hub]++
	}
	capIn := max(4, p.Hub/4)
	for i, n := range pl.nodes {
		if n.noOut || n.island {
			continue
		}
		want := deg[i]
		if n.expand {
			// An expanded concept lists its parts; its other links fill up to the draw.
			want = max(0, want-len(n.sats))
		}
		seen := map[int]bool{i: true}
		for _, t := range n.out {
			seen[t] = true
		}
		tries := 0
		for len(n.out) < want && tries < 60 {
			tries++
			var j int
			if pl.r.chance(0.65) && len(pl.byMap[n.mi]) > 1 {
				j = pl.byMap[n.mi][pl.r.n(len(pl.byMap[n.mi]))]
			} else {
				j = pl.r.n(N)
			}
			if seen[j] || j == pl.hub || pl.nodes[j].island || pl.inDeg[j] >= capIn || pl.nodes[j].retired() {
				continue
			}
			seen[j] = true
			n.out = append(n.out, j)
			pl.inDeg[j]++
		}
	}
	// A concept with no link out is still linked to: a node connected to nothing
	// is an orphan, a finding no profile plants.
	for i, n := range pl.nodes {
		if !n.noOut || pl.inDeg[i] > 0 || n.retired() {
			continue
		}
		for t := 0; t < 80; t++ {
			s := pl.r.n(N)
			if s == i || pl.nodes[s].noOut || pl.nodes[s].island || pl.nodes[s].retired() {
				continue
			}
			pl.nodes[s].out = append(pl.nodes[s].out, i)
			pl.inDeg[i]++
			break
		}
	}
	// A planted broken link, three of them.
	if p.Plant {
		for k := 0; k < 3 && k < N; k++ {
			n := pl.nodes[(k*37+11)%N]
			if !n.island {
				n.broken = pl.g.maps[n.mi].slug + "/missing-" + fmt.Sprint(k+1)
			}
		}
	}
}

func (n *pnode) retired() bool {
	return n.status == "archived" || n.status == "deprecated" || n.status == "superseded"
}

func (pl *planner) shuffle(xs []int) {
	for i := len(xs) - 1; i > 0; i-- {
		j := pl.r.n(i + 1)
		xs[i], xs[j] = xs[j], xs[i]
	}
}

// statuses gives every concept its status, the profile's mix followed exactly
// (scaled to the number of concepts, by largest remainder). Statuses go where
// they read right first: none to journal entries, a custom vocabulary to its
// Map, decision statuses to decisions, work statuses to work.
func (pl *planner) statuses() {
	p, th := pl.p, pl.th
	N := len(pl.nodes)
	counts := apportion(p.Status, N)
	assigned := make([]bool, N)
	set := func(i int, s string) {
		assigned[i] = true
		if s != "none" {
			pl.nodes[i].status = s
		}
	}
	order := func(prefer func(n *pnode) bool) []int {
		var a, b []int
		for i, n := range pl.nodes {
			if assigned[i] {
				continue
			}
			if prefer != nil && prefer(n) {
				a = append(a, i)
			} else {
				b = append(b, i)
			}
		}
		pl.shuffle(a)
		pl.shuffle(b)
		return append(a, b...)
	}
	take := func(status string, n int, prefer func(n *pnode) bool, sorted bool) {
		cands := order(prefer)
		if sorted { // journal entries in order: the first ones fill first
			var pref []int
			for _, i := range cands {
				if prefer(pl.nodes[i]) {
					pref = append(pref, i)
				}
			}
			sort.Ints(pref)
			var rest []int
			for _, i := range cands {
				if !prefer(pl.nodes[i]) {
					rest = append(rest, i)
				}
			}
			cands = append(pref, rest...)
		}
		for _, i := range cands {
			if n == 0 {
				return
			}
			if status == "custom" {
				set(i, th.custom[pl.r.n(len(th.custom))])
			} else {
				set(i, status)
			}
			n--
		}
	}
	isMap := func(slug string) func(n *pnode) bool {
		return func(n *pnode) bool { return th.maps[n.mi].slug == slug }
	}
	byRole := func(roles string) func(n *pnode) bool {
		return func(n *pnode) bool { return strings.IndexByte(roles, n.role) >= 0 }
	}
	count := func(name string) int {
		for i, m := range p.Status {
			if m.Status == name {
				return counts[i]
			}
		}
		return 0
	}
	notJournal := func(n *pnode) bool { return strings.IndexByte("LSQ", n.role) < 0 }
	take("none", count("none"), byRole("LSQ"), true)
	if th.customMap != "" {
		take("custom", count("custom"), isMap(th.customMap), false)
	}
	adr := byRole("A")
	work := byRole("W")
	for _, s := range []string{"accepted", "superseded", "rejected"} {
		take(s, count(s), adr, false)
	}
	for _, s := range []string{"done", "completed", "open", "in-progress", "blocked", "proposed"} {
		take(s, count(s), work, false)
	}
	take("reference", count("reference"), byRole("R"), false)
	for i, m := range p.Status {
		switch m.Status {
		case "none", "custom", "accepted", "superseded", "rejected", "done", "completed", "open", "in-progress", "blocked", "proposed", "reference":
			continue
		}
		take(m.Status, counts[i], notJournal, false)
	}
	// Anything left (rounding) is active, or none in a journal.
	for i := range pl.nodes {
		if !assigned[i] {
			if byRole("LSQ")(pl.nodes[i]) {
				set(i, "none")
			} else {
				set(i, "active")
			}
		}
	}
	// What is open is recent: the profile has no stale work in it.
	for _, n := range pl.nodes {
		switch n.status {
		case "open", "in-progress", "proposed", "blocked", "draft":
			n.date = pl.r.n(25)
		}
	}
	// A superseded concept points at its successor in the same Map.
	for i, n := range pl.nodes {
		if n.status != "superseded" {
			continue
		}
		for _, j := range pl.byMap[n.mi] {
			if j != i && pl.nodes[j].status != "superseded" && pl.nodes[j].status != "archived" && pl.nodes[j].status != "deprecated" {
				n.supersedes = pl.nodes[j].id
				break
			}
		}
	}
	// Placeholders.
	if p.Placeholders > 0 {
		for k := 0; k < p.Placeholders; k++ {
			pl.nodes[pl.r.n(N)].phs++
		}
	}
	// Sources: every ingested one is cited by a concept, and some concepts cite one.
	var srcNodes, citers []int
	for i, n := range pl.nodes {
		switch {
		case n.role == roleSource:
			srcNodes = append(srcNodes, i)
		case n.role == roleTopic || n.role == roleADR:
			citers = append(citers, i)
		}
	}
	if len(srcNodes) > 0 && len(citers) > 0 {
		for _, s := range srcNodes {
			if pl.nodes[s].ingest == "ingested" {
				c := pl.nodes[citers[pl.r.n(len(citers))]]
				c.cites = append(c.cites, pl.nodes[s].id)
			}
		}
		for _, c := range citers {
			if pl.r.chance(0.12) {
				pl.nodes[c].cites = append(pl.nodes[c].cites, pl.nodes[srcNodes[pl.r.n(len(srcNodes))]].id)
			}
		}
	}
	// Frontmatter keys.
	for _, n := range pl.nodes {
		n.keys = pl.keyCount()
	}
	pl.nodes[pl.r.n(N)].keys = p.FMMax
}

func sopsName(k int) string { return fmt.Sprintf("store-%d", k+1) }

func (pl *planner) keyCount() int {
	p := pl.p
	d := p.FMMax - p.FMP50
	r := pl.r.f()
	k := p.FMP50
	switch {
	case d == 0 || r < 0.5:
	case r < 0.75:
		k--
	default:
		k += 1 + pl.r.n(min(d, 3))
	}
	return max(3, min(k, p.FMMax))
}

// sizes draws the byte target of every concept and satellite from a log-normal
// of the profile, then forces the extremes: the concepts past 90 KB, the one at
// the largest size.
func (pl *planner) sizes() {
	p := pl.p
	ceil := float64(min(p.BytesMax, 88000))
	for _, n := range pl.nodes {
		sz := pl.r.logn(float64(p.BytesP50), float64(p.BytesP90))
		if n.role == roleRef {
			sz *= 0.25
		}
		n.size = int(math.Max(300, math.Min(sz, ceil*0.95)))
		for _, s := range n.sats {
			sz := pl.r.logn(float64(p.BytesP50)*0.5, float64(p.BytesP90)*0.5)
			s.size = int(math.Max(250, math.Min(sz, 20000)))
		}
	}
	var cand []int
	for i, n := range pl.nodes {
		if n.role == roleTopic && !pl.journal[n.mi] && !n.island && i != pl.hub {
			cand = append(cand, i)
		}
	}
	if len(cand) == 0 {
		for i := range pl.nodes {
			cand = append(cand, i)
		}
	}
	pl.shuffle(cand)
	if p.Over90 == 0 {
		pl.nodes[cand[0]].size = p.BytesMax
		return
	}
	for k := 0; k < p.Over90 && k < len(cand); k++ {
		pl.nodes[cand[k]].size = 93000 + pl.r.n(max(1, p.BytesMax-93000))
	}
	pl.nodes[cand[0]].size = p.BytesMax
}

func (pl *planner) nodePath(n *pnode) string {
	if n.expand {
		return "data/" + n.id + "/index.md"
	}
	return "data/" + n.id + ".md"
}

// rel is the markdown link from a file to the concept id (an expanded concept
// by its id, as the link rule wants).
func rel(from, toID string) string {
	r, _ := filepath.Rel(filepath.Dir(from), "data/"+toID+".md")
	return filepath.ToSlash(r)
}

// fmFields is the frontmatter of a concept, padded with the generic keys up to
// its key count.
func (pl *planner) fmFields(n *pnode, specials ...string) []string {
	f := []string{"type", n.typ, "title", n.title}
	if n.status != "" {
		f = append(f, "status", n.status)
	}
	f = append(f, "timestamp", pl.g.date(n.date))
	f = append(f, specials...)
	if len(n.cites) > 0 {
		f = append(f, "provenance", list(dedupe(append([]string(nil), n.cites...), "")...))
	}
	if n.supersedes != "" {
		f = append(f, "superseded_by", n.supersedes)
	}
	if n.secret != "" {
		f = append(f, "secrets_source", list(n.secret))
	}
	have := len(f) / 2
	if pl.p.Plant && n.mi == 1 && n.keys <= have {
		n.keys = have + 1 // the planted required_fields contract wants a description
	}
	for _, k := range extraKeys {
		if have >= n.keys {
			break
		}
		f = append(f, k, pl.extraValue(k, n))
		have++
	}
	return f
}

func (pl *planner) extraValue(k string, n *pnode) string {
	switch k {
	case "description":
		return titleCase(pl.r.pick(pl.th.adjs)) + " " + pl.r.pick(pl.th.nouns) + " as the " + strings.ToLower(pl.th.maps[n.mi].title) + " Map uses it."
	case "area":
		return pl.th.maps[n.mi].slug
	case "owner", "reviewer", "audience":
		return pl.r.pick(fillerRoles)
	case "origin":
		return pl.r.pick(pl.th.nouns)
	case "confidence":
		return pl.r.pick(fillerLevels)
	case "scope":
		return pl.r.pick(pl.th.adjs)
	case "summary":
		return pl.r.pick(pl.th.adjs) + " " + pl.r.pick(pl.th.nouns)
	case "cycle":
		return pl.r.pick(fillerCycles)
	}
	return fmt.Sprint(1 + pl.r.n(30))
}

// bodyOf assembles a concept's body to its byte target. headings are the
// section titles of its role; links come out inline and in a closing Links
// section; tail is role-specific text that ends the last section.
func (pl *planner) bodyOf(n *pnode, from string, headings []string, rest int, outIDs []string, inlineN int, partIDs [][2]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n# %s\n\n", n.title)
	// The links come first, so the sections can fill what is left.
	var inline, closing strings.Builder
	half := min(len(outIDs), max((len(outIDs)+1)/2, inlineN))
	if half > 0 {
		inline.WriteString("See also ")
		for i, id := range outIDs[:half] {
			if i > 0 {
				inline.WriteString(", ")
			}
			inline.WriteString("[[" + id + "]]")
		}
		inline.WriteString(".\n")
	}
	if n.broken != "" {
		inline.WriteString("It once pointed at [[" + n.broken + "]].\n")
	}
	if len(outIDs) > half {
		closing.WriteString("\n## Links\n\n")
		for _, id := range outIDs[half:] {
			fmt.Fprintf(&closing, "- [%s](%s) — the %s %s\n", pl.titleOfID(id), rel(from, id), pl.r.pick(pl.th.adjs), pl.r.pick(pl.th.nouns))
		}
	}
	if len(partIDs) > 0 {
		closing.WriteString("\n## Parts\n\n")
		for _, pt := range partIDs {
			fmt.Fprintf(&closing, "- [%s](%s)\n", pt[0], pt[1])
		}
	}
	ph := ""
	for i := 0; i < n.phs; i++ {
		switch {
		case i%2 == 0 && len(pl.th.paths) > 0:
			key := pl.th.paths[i/2%len(pl.th.paths)]
			pl.cited["path:"+key] = true
			ph += fmt.Sprintf(" The shared folder is {{path:%s}}.", key)
		case len(pl.th.repos) > 0:
			key := pl.th.repos[i/2%len(pl.th.repos)]
			pl.cited["repo:"+key] = true
			ph += fmt.Sprintf(" The tooling lives in {{repo:%s}}.", key)
		}
	}
	rest -= b.Len() + inline.Len() + closing.Len() + len(ph)
	rest = max(rest, 0)
	nsec := len(headings)
	if nsec == 0 {
		b.WriteString(pl.paragraph(rest))
		b.WriteString(ph)
		b.WriteString("\n\n")
	} else {
		per := rest / nsec
		for i, h := range headings {
			if i == 0 {
				b.WriteString(pl.paragraph(60) + ph + "\n\n")
			}
			lvl := "##"
			if i%3 == 2 {
				lvl = "###"
			}
			fmt.Fprintf(&b, "%s %s\n\n", lvl, h)
			b.WriteString(pl.section(per))
			b.WriteString("\n\n")
		}
	}
	b.WriteString(inline.String())
	b.WriteString(closing.String())
	return b.String()
}

func (pl *planner) titleOfID(id string) string {
	for _, n := range pl.nodes {
		if n.id == id {
			return n.title
		}
	}
	return id
}

// section is a few paragraphs, a table or a code fence in a share of them.
func (pl *planner) section(minBytes int) string {
	var b strings.Builder
	for b.Len() < minBytes-40 || b.Len() == 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		switch x := pl.r.f(); {
		case x < 0.08 && minBytes > 300:
			b.WriteString("| " + fillerTable[0] + " | " + fillerTable[1] + " | " + fillerTable[2] + " |\n|---|---|---|\n")
			for r := 0; r < 3; r++ {
				fmt.Fprintf(&b, "| %s | %d | %s |\n", pl.r.pick(pl.th.nouns), 1+pl.r.n(90), pl.r.pick(pl.th.adjs))
			}
		case x < 0.14 && minBytes > 300:
			fmt.Fprintf(&b, "```sh\n%s --%s %s --%s %s\n```\n", fillerCode[0], fillerCode[1], pl.r.pick(pl.th.nouns), fillerCode[2], pl.r.pick(fillerPeriods))
		default:
			b.WriteString(pl.paragraph(min(160, max(minBytes-b.Len(), 1))))
		}
	}
	return b.String()
}

// headingsFor are the section titles of a concept by its Map's role.
func (pl *planner) headingsFor(n *pnode) []string {
	switch n.role {
	case roleADR:
		return adrHeadings
	case roleLog:
		return logHeadings
	case roleRef, roleSource, roleQuestion:
		return nil
	case roleWork:
		return []string{"Context"}
	}
	hs := int(math.Round(float64(pl.p.Headings) * math.Sqrt(float64(n.size)/float64(pl.p.BytesP50)) * (0.6 + 0.8*pl.r.f())))
	hs = max(0, min(hs, 60, n.size/400))
	out := make([]string, hs)
	for i := range out {
		out[i] = fillerHeadings[i%len(fillerHeadings)]
		if i >= len(fillerHeadings) {
			out[i] += fmt.Sprintf(" %d", i/len(fillerHeadings)+1)
		}
	}
	return out
}

// emit writes the concepts, their satellites and assets, and the Map contracts.
func (pl *planner) emit() {
	g, p := pl.g, pl.p
	for _, n := range pl.nodes {
		from := pl.nodePath(n)
		// Concepts that link back are named inline, where the backlink does not
		// duplicate them (reciprocal_link_item).
		var outIDs []string
		var rest []string
		for _, j := range n.out {
			back := false
			for _, b := range pl.nodes[j].out {
				back = back || pl.nodes[b] == n
			}
			if back {
				outIDs = append(outIDs, pl.nodes[j].id)
			} else {
				rest = append(rest, pl.nodes[j].id)
			}
		}
		outIDs = append(outIDs, n.svc...) // by id only: a service is not under data/
		recip := len(outIDs)
		outIDs = append(outIDs, rest...)
		var parts [][2]string
		for _, s := range n.sats {
			parts = append(parts, [2]string{titleCase(s.id[strings.LastIndex(s.id, "/")+1:]), s.id[strings.LastIndex(s.id, "/")+1:] + ".md"})
		}
		var specials []string
		switch n.role {
		case roleSource:
			specials = []string{"source_kind", sourceKinds[n.mi%len(sourceKinds)], "locator", fmt.Sprintf("https://example.com/reports/%d", len(g.files)),
				"ingest_status", n.ingest}
		case roleQuestion:
			specials = []string{"contradiction_kind", []string{"measurement", "definition", "open_question", "missing_context"}[len(g.files)%4],
				"resolution_status", []string{"open", "open", "resolved"}[len(g.files)%3], "reason", "The two values do not overlap."}
			if len(outIDs) >= 2 {
				specials = append(specials, "involves", list(outIDs[0], outIDs[1]))
			}
		case roleWork:
			specials = []string{"priority", fmt.Sprintf("P%d", len(g.files)%4)}
			if n.status == "blocked" {
				specials = append(specials, "waiting_on", "an answer from the "+pl.r.pick(fillerRoles), "review_after", g.date(-7))
			}
		}
		fields := pl.fmFields(n, specials...)
		headings := pl.headingsFor(n)
		body := pl.bodyOf(n, from, headings, n.size-len(fm(fields...)), outIDs, recip, parts)
		body += pl.tail(n)
		if len(n.assets) > 0 {
			var att strings.Builder
			att.WriteString("\n## Attachments\n\n")
			for i, a := range n.assets {
				if p.Plant && i == len(n.assets)-1 && n == pl.nodes[pl.hub] {
					continue // an asset nothing references
				}
				fmt.Fprintf(&att, "- [%s](%s)\n", a.name, a.name)
			}
			body += att.String()
		}
		f := g.add(from, fm(fields...)+body, unborn)
		if n.retired() {
			g.retired = append(g.retired, n.id)
		}
		switch n.status {
		case "deprecated", "archived":
			f.reason = "retired: no longer in use"
		case "superseded":
			f.reason = "retired: no longer in use"
			if n.supersedes != "" {
				f.reason = "supersede: " + n.id + " -> " + n.supersedes
			}
		}
		for _, s := range n.sats {
			sp := "data/" + s.id + ".md"
			t := titleCase(strings.ReplaceAll(s.id[strings.LastIndex(s.id, "/")+1:], "-", " "))
			sf := []string{"type", n.typ, "title", t + " part", "timestamp", g.date(n.date), "description", "A part of " + n.title + "."}
			text := fm(sf...) + fmt.Sprintf("\n# %s part\n\nA part of [[%s]].\n\n%s\n", t, n.id, pl.paragraph(max(0, s.size-120)))
			g.add(sp, text, unborn)
		}
		for _, a := range n.assets {
			g.add("data/"+n.id+"/"+a.name, a.content, unborn)
		}
	}
	pl.contracts()
	if p.Plant {
		g.defectsIn(1, 2)
	}
}

// tail ends a concept in what its role needs: the checklist of work, a done
// concept's with one item left open when the profile plants findings.
func (pl *planner) tail(n *pnode) string {
	if n.role == roleRef || n.role == roleSource || n.role == roleQuestion {
		return ""
	}
	done := n.status == "done" || n.status == "completed"
	switch {
	case done:
		if pl.p.Plant && n.date%2 == 0 {
			return "\n## Definition of done\n\n- [x] Re-read the entries\n- [ ] Tell the readers\n"
		}
		return "\n## Definition of done\n\n- [x] Everything\n"
	case n.role == roleWork:
		return "\n## Definition of done\n\n- [x] Collect the entries\n- [ ] Fix the broken ones\n"
	}
	return ""
}

// contracts writes the Map descriptors: the contract of each, from its theme.
func (pl *planner) contracts() {
	g, p := pl.g, pl.p
	for mi := range p.Maps {
		tmap := pl.th.maps[mi]
		kind := "map"
		if pl.journal[mi] {
			kind = "journal"
		}
		types := pl.types[mi]
		f := []string{"type", "Map", "title", tmap.title, "description", "Concepts about " + strings.ToLower(tmap.title) + ".", "kind", kind,
			"concept_types", list(types...)}
		used := map[string]bool{}
		for _, i := range pl.byMap[mi] {
			if s := pl.nodes[i].status; s != "" {
				used[s] = true
			}
		}
		var vocab []string
		for s := range used {
			vocab = append(vocab, s)
		}
		sort.Strings(vocab)
		if tmap.slug == pl.th.customMap || (p.Plant && mi == 1) {
			// The Map's own status vocabulary: what it uses, and (planted) nothing else.
			f = append(f, "field_values.*.status", list(vocab...))
		}
		if tmap.role == roleWork {
			f = append(f, "open_statuses", list("open", "in-progress", "blocked"))
		}
		if p.Plant {
			switch mi {
			case 1:
				f = append(f, "required_fields.*", list("description"), "forbidden_title_terms", list("misc", "untitled"))
			case 4:
				f = append(f, "stale_after", "soon") // contract_malformed
			}
		}
		f = append(f, "timestamp", g.date(180))
		g.add("data/"+tmap.slug+"/_map.md", fm(f...)+"\n# "+tmap.title+"\n", 0)
	}
}

// name is the file name of the k-th artifact.
func (pl *planner) name(k int) string {
	return pl.th.nouns[k%len(pl.th.nouns)] + fmt.Sprintf("-%d", k/len(pl.th.nouns)+1)
}

// artifacts writes the skills, agents, hooks, services and secrets of the
// profile's counts, and the placeholder registry.
func (pl *planner) artifacts(c int) {
	g, p, th := pl.g, pl.p, pl.th
	pick := func() string { return pl.nodes[pl.r.n(len(pl.nodes))].id }
	name := pl.name
	for i := 0; i < p.Skills; i++ {
		ref := pick()
		if p.Plant && i == p.Skills-1 {
			ref = pl.g.maps[0].slug + "/no-such-concept" // skill_broken_ref
		}
		g.add(fmt.Sprintf("skills/%s/SKILL.md", name(i)), fmt.Sprintf("---\nname: %s\ndescription: Skill for the %s, reads a few concepts first\n---\n# %s\n\nRead first:\n\n- [[%s]]\n- [[%s]]\n", name(i), th.nouns[i%len(th.nouns)], name(i), pick(), ref), c)
	}
	for i := 0; i < p.Agents; i++ {
		g.add(fmt.Sprintf("agents/%s.md", name(i+100)), fmt.Sprintf("---\nname: %s\ndescription: Agent for the %s\n---\nYou review.\n\n- [[%s]]\n", name(i+100), th.nouns[(i+7)%len(th.nouns)], pick()), c)
	}
	for i := 0; i < p.Hooks; i++ {
		g.add(fmt.Sprintf("hooks/%s/hook.json", name(i+200)), `{"event":"SessionStart","command":"run.sh"}`+"\n", c)
		g.add(fmt.Sprintf("hooks/%s/run.sh", name(i+200)), "#!/bin/sh\necho 'Read [["+pick()+"]] before you start.'\n", c)
	}
	if p.Plant {
		g.add("hooks/broken/hook.json", `{"event":"NoSuchEvent"}`+"\n", c)
	}
	if len(pl.cited) > 0 {
		var y strings.Builder
		y.WriteString("paths:\n")
		for k, key := range th.paths {
			if !pl.cited["path:"+key] {
				continue
			}
			d := "~/shared"
			if g.spec.Name == "observatory" && k == 0 {
				d = "~/Shared Files" // two KBs declaring the same key with different defaults
			}
			fmt.Fprintf(&y, "  %s: {description: a shared folder, default: %s}\n", key, d)
		}
		y.WriteString("repos:\n")
		for _, key := range th.repos {
			if !pl.cited["repo:"+key] {
				continue
			}
			fmt.Fprintf(&y, "  %s: {description: scripts the team runs, remote: https://gitlab.example.com/tools/%s.git}\n", key, key)
		}
		g.add("paths.yaml", y.String(), c)
	}
	if p.SopsFiles > 0 {
		g.add(".sops.yaml", "creation_rules:\n  - path_regex: secrets/.*\\.sops\\.yaml$\n    age: age1testtesttesttesttesttesttesttesttesttesttesttesttesttestqqqq\n", c)
		for k := 0; k < p.SopsFiles; k++ {
			g.add(fmt.Sprintf("secrets/%s.sops.yaml", sopsName(k)), sopsFile("token: "+enc("tok")+"\nuser: "+enc("usr")+"\npassword: "+enc("pwd")+"\n"), c)
		}
	}
	for i := 0; i < p.Services; i++ {
		f := sopsName(i % max(1, p.SopsFiles))
		g.add(fmt.Sprintf("services/%s.md", name(i+300)), fm("type", "Service", "title", titleCase(th.nouns[(i+3)%len(th.nouns)])+" service", "description", "Where the work is tracked.", "kind", "api",
			"base_url", "https://tickets.example.com/", "status", "active", "secret_refs", list("SERVICE_TOKEN=secrets/"+f+".sops.yaml#/token"))+
			"\n# "+titleCase(th.nouns[(i+3)%len(th.nouns)])+" service\n\nA service the team calls, described in [["+pick()+"]].\n", c)
	}
}

// historyProfile spreads the births over batches and gives the rest of the
// profile's commits to revisions, most of them of a hot set (one concept in
// ten), plus a move, a delete, the supersedes and two repairs, all late. The
// commits' times are dealt out from the profile's span when they are written.
func (g *kbgen) historyProfile() {
	p := g.spec.Profile
	var births []*file
	for _, f := range g.files {
		if f.born == unborn {
			births = append(births, f)
		}
	}
	for i := len(births) - 1; i > 0; i-- {
		j := g.r.n(i + 1)
		births[i], births[j] = births[j], births[i]
	}
	var concepts []*file
	for _, f := range births {
		if strings.HasPrefix(f.path, "data/") && strings.HasSuffix(f.path, ".md") {
			concepts = append(concepts, f)
		}
	}
	special := 0
	supersedes := 0
	for _, f := range g.files {
		if strings.HasPrefix(f.reason, "supersede: ") && supersedes < 3 {
			supersedes++
		}
	}
	if len(concepts) > 10 {
		special = 2 + 2 + supersedes // move, delete, two repairs
	}
	nBirth := max(6, min(len(births), p.Commits/5))
	chunk := (len(births) + nBirth - 1) / nBirth
	batches := (len(births) + chunk - 1) / chunk
	nPatch := max(0, p.Commits-1-batches-special)
	bornFrac := map[*file]float64{}
	for i := 0; i < len(births); i += chunk {
		end := min(len(births), i+chunk)
		subject := "concept_batch: " + fmt.Sprint(end-i) + " concepts"
		if end-i == 1 {
			subject = "concept_write: " + strings.TrimSuffix(strings.TrimPrefix(births[i].path, "data/"), ".md")
		}
		frac := 0.02 + 0.86*float64(i)/float64(len(births))
		c := g.commitAt(subject, "filed from the survey notes", frac)
		for _, f := range births[i:end] {
			f.born = c
			bornFrac[f] = frac
		}
	}
	// Revisions: a hot set takes most of them.
	var eligible []*file
	for _, f := range concepts {
		if f.reason == "" && len(f.versions[0]) <= 3*p.BytesP90 && !isDefect(f.path) {
			eligible = append(eligible, f)
		}
	}
	if len(eligible) == 0 {
		return
	}
	hot := eligible[:max(1, min(len(eligible), max(3, len(eligible)/10)))]
	reasons := []string{"fixed a wrong value", "added a source", "clarified the definition", "split a long paragraph"}
	for i := 0; i < nPatch; i++ {
		pool := eligible
		if g.r.chance(0.7) {
			pool = hot
		}
		f := pool[g.r.n(len(pool))]
		id := strings.TrimSuffix(strings.TrimPrefix(f.path, "data/"), ".md")
		bf := bornFrac[f]
		c := g.commitAt("concept_patch: "+id, reasons[g.r.n(len(reasons))], bf+(0.98-bf)*g.r.f())
		f.versions = append(f.versions, f.versions[len(f.versions)-1]+fmt.Sprintf("\nRevision note %d.\n", len(f.versions)))
		f.revs = append(f.revs, c)
	}
	if len(concepts) <= 10 {
		return
	}
	// A move, a delete and the supersedes, all late.
	// (one nothing links to, so no link is left pointing at the old name)
	for k := range eligible {
		mv := eligible[(len(eligible)/2+k)%len(eligible)]
		if strings.HasSuffix(mv.path, "/index.md") || g.referenced[idOf(mv.path)] || strings.Count(idOf(mv.path), "/") != 1 {
			continue
		}
		to := strings.TrimSuffix(mv.path, ".md") + "-renamed.md"
		from := mv.path
		c := g.commitAt("concept_move: "+idOf(from)+" -> "+idOf(to), "a clearer name", 0.985)
		mv.movedFrom, mv.path, mv.moveAt = from, to, c
		if idx := g.byPath["data/"+idOf(from)[:strings.Index(idOf(from), "/")]+"/index.md"]; idx != nil {
			idx.versions[0] = strings.ReplaceAll(idx.versions[0], "[["+idOf(from)+"]]", "[["+idOf(to)+"]]")
		}
		break
	}
	del := g.add("data/"+g.maps[0].slug+"/to-be-deleted.md", fm("type", "Note", "title", "To be deleted")+"\n# To be deleted\n", births[0].born)
	del.deleteAt = g.commitAt("concept_delete: "+idOf(del.path), "a duplicate of another concept", 0.99)
	k := 0
	for _, f := range g.files {
		if strings.HasPrefix(f.reason, "supersede: ") && k < supersedes {
			k++
			f.revs = append(f.revs, g.commitAt(f.reason, "a better concept covers it", 0.9+0.09*g.r.f()))
			f.versions = append(f.versions, f.versions[len(f.versions)-1]+"\nSuperseded.\n")
		}
	}
	for _, r := range [][3]string{
		{"auto-repair (background): duplicate_link", "auto-repair: removed a duplicated link", "0.995"},
		{"kb_repair: index_incomplete", "kb_repair: listed missing concepts in the index", "0.997"},
	} {
		f := eligible[g.r.n(len(eligible))]
		for t := 0; (f.movedFrom != "" || f.deleteAt > 0) && t < len(eligible); t++ {
			f = eligible[(g.r.n(len(eligible))+t)%len(eligible)]
		}
		var frac float64
		fmt.Sscan(r[2], &frac)
		f.revs = append(f.revs, g.commitAt(r[0], r[1], frac))
		f.versions = append(f.versions, f.versions[len(f.versions)-1]+"\nRepaired.\n")
	}
}

// isDefect says whether a path is one of the planted defects of defectsIn.
func isDefect(path string) bool {
	base := strings.TrimSuffix(filepath.Base(path), ".md")
	for _, d := range defectPaths {
		if base == strings.TrimSuffix(d, ".md") || base == d {
			return true
		}
	}
	return false
}
