package kb

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// The link graph as a stat-validated in-memory cache (D241).
//
// D108's guarantee — the graph always follows the files — is kept by
// validation, not by notification: every full graph access enumerates the
// concept files, stats them (the walk's DirEntry.Info is an lstat) and
// re-reads only the files whose signature changed. Nothing is persisted and
// nothing depends on a write path remembering to invalidate: an edit made by
// a handler, a git pull or an operator's editor is seen the same way.
//
// What is cached per file is what the graph readers need — links, the asset
// probes that decided them, the content hash and four frontmatter facets —
// never the body, so memory stays proportional to the link count.

// racyWindow is how close to its observation a modification time may be
// before the signature stops being trusted. A file rewritten within the same
// timestamp tick, keeping its size, has an unchanged signature; git calls
// such a file "racily clean" and re-reads it. Two seconds covers FAT's
// granularity, the coarsest a KB is likely to sit on. A variable only so a
// test can reach the reuse path for a file it cannot backdate (a symlink).
var racyWindow = 2 * time.Second

// conceptFile is one file the concept walk emits, before it is read.
type conceptFile struct {
	id  okf.ConceptID
	rel string // KB-relative path, as ReadRaw takes it
	// entry is the walk's directory entry: Info() is an lstat, Type() tells a
	// symlink apart.
	entry fs.DirEntry
}

// conceptFiles enumerates exactly the files walkConceptPaths emits, in its
// order, without reading them. It is the one place the walk's rules live:
// data/ plus services/, only map/concept/index.md among index files (as
// map/concept), reserved names skipped.
func (kb *KB) conceptFiles() ([]conceptFile, error) {
	var files []conceptFile
	dataRoot, err := kb.ResolvePath(".", false)
	if err != nil {
		return nil, err
	}
	err = filepath.WalkDir(dataRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			rel, _ := filepath.Rel(kb.DataRoot(), p)
			files = append(files, conceptFile{rel: filepath.ToSlash(rel), entry: d})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	servicesDir := filepath.Join(kb.Root, "services")
	if _, statErr := os.Stat(servicesDir); !os.IsNotExist(statErr) {
		err = filepath.WalkDir(servicesDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, ".md") {
				rel, _ := filepath.Rel(kb.Root, p)
				files = append(files, conceptFile{rel: filepath.ToSlash(rel), entry: d})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	out := files[:0]
	for _, f := range files {
		base := path.Base(f.rel)
		if base == "index.md" {
			dir := path.Dir(f.rel)
			if dir == "." || len(strings.Split(dir, "/")) != 2 {
				// Root index.md and map-level index.md ("map/index.md")
				// stay reserved/excluded — only an expanded concept's
				// index.md ("map/concept/index.md") is emitted.
				continue
			}
			f.id = okf.ConceptID(dir)
			out = append(out, f)
			continue
		}
		if okf.IsReserved(base) {
			continue
		}
		f.id = okf.ConceptID(strings.TrimSuffix(f.rel, ".md"))
		out = append(out, f)
	}
	return out, nil
}

// fileSig is what a file's cache entry is validated against. The mode is
// part of it because a chmod changes neither size nor mtime, yet decides
// whether the uncached walk can read the file at all.
type fileSig struct {
	size    int64
	modTime int64 // nanoseconds
	mode    fs.FileMode
}

// ConceptFacets are the frontmatter fields graph readers and the policy use.
type ConceptFacets struct {
	// Exists is false for an id no file holds (or one that cannot be read).
	Exists bool
	// HasFrontmatter and Parsed say whether a frontmatter block was found and
	// whether it parsed; the string facets are empty unless Parsed.
	HasFrontmatter bool
	Parsed         bool
	Type           string
	Title          string
	Status         string
	SupersededBy   string
	// Placeholders are the {{repo:…}}/{{path:…}} keys the body cites, as
	// sorted, unique "kind:key" strings (okf.Placeholders: escaped ones and
	// metasyntax excluded). Cached with the rest of the entry, so sync_pull
	// lists a KB's keys without re-reading a file (D262). Independent of
	// whether the frontmatter parsed: a body cites what it cites.
	Placeholders []string
}

func facetsOf(content string) ConceptFacets {
	fmRaw, body, hasFM := okf.SplitFrontmatter(content)
	f := ConceptFacets{Exists: true, HasFrontmatter: hasFM, Placeholders: okf.Placeholders(body)}
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil {
		return f
	}
	f.Parsed = true
	f.Type = fm.Type()
	if v, ok := fm.Get("title"); ok {
		f.Title, _ = v.(string)
	}
	if v, ok := fm.Get("status"); ok {
		f.Status, _ = v.(string)
	}
	if v, ok := fm.Get("superseded_by"); ok {
		f.SupersededBy, _ = v.(string)
	}
	return f
}

// graphEntry is the parse result of one physical file.
type graphEntry struct {
	id       okf.ConceptID
	rel      string
	sig      fileSig
	observed time.Time
	// links is ExtractLinks' result, in its order.
	links []okf.ConceptID
	// probes are the asset-resolver answers that decided links: an href is a
	// concept link or an asset citation depending on whether a file exists,
	// so a new or deleted asset changes the links without touching this file.
	probes map[string]bool
	hash   string
	facets ConceptFacets
}

// trusted reports whether e may be reused for a file now signed sig.
func (e *graphEntry) trusted(sig fileSig) bool {
	return e.sig == sig && time.Unix(0, e.sig.modTime).Before(e.observed.Add(-racyWindow))
}

// graphView is an immutable derived view: published once, never mutated, so
// readers traverse it without holding the cache lock.
type graphView struct {
	generation uint64
	// entries are the emitted files in walk order: GraphSnapshot replays them
	// exactly as it replayed the walk.
	entries []*graphEntry
	adj     linkAdjacency
	exists  map[okf.ConceptID]struct{}
	// facets per id; when two files emit one id, the later in walk order
	// wins, as GraphSnapshot's replay does.
	facets map[okf.ConceptID]ConceptFacets
}

type graphCache struct {
	entries map[string]*graphEntry // by rel
	// facetOnly holds single-file facet lookups (ConceptFacets). Kept apart
	// from entries: refreshing a full entry outside a validation would leave
	// the published view stale while the next validation saw nothing to do.
	facetOnly map[string]*graphEntry
	view      *graphView
	stats     graphCacheStats
	// reported is, per id, the content hash ConceptChanges last reported
	// (D245). nil until the first call or a seed.
	reported map[okf.ConceptID]string
}

// graphCacheStats counts the work of the last full validation and the facet
// lookups, for tests that assert a warm read parses nothing.
type graphCacheStats struct {
	validations int
	parsed      int
	reused      int
	facetReads  int
	// reads counts every file read the cache makes, over its lifetime.
	reads int
}

// GraphValidations reports how many full validations of the link-graph cache
// this KB has run. It exists so tests outside this package can assert that a
// reader validates once per call rather than once per concept (D241).
func (kb *KB) GraphValidations() int {
	kb.graphMu.Lock()
	defer kb.graphMu.Unlock()
	if kb.graph == nil {
		return 0
	}
	return kb.graph.stats.validations
}

// GraphFileReads reports how many concept files the link-graph cache has read
// over its lifetime: validations, facet lookups and change notifications. It
// exists so tests outside this package can assert that a warm read touches no
// file (D241, D245).
func (kb *KB) GraphFileReads() int {
	kb.graphMu.Lock()
	defer kb.graphMu.Unlock()
	if kb.graph == nil {
		return 0
	}
	return kb.graph.stats.reads
}

func (kb *KB) graphCacheLocked() *graphCache {
	if kb.graph == nil {
		kb.graph = &graphCache{entries: map[string]*graphEntry{}, facetOnly: map[string]*graphEntry{}}
	}
	return kb.graph
}

// graphView validates the cache against the files and returns the current
// view, building a new one when anything changed.
func (kb *KB) graphView() (*graphView, error) {
	kb.graphMu.Lock()
	defer kb.graphMu.Unlock()
	return kb.graphViewLocked(nil)
}

// graphViewLocked is graphView with graphMu held. When read is non-nil, every
// file the validation reads is recorded in it by rel, so ConceptChanges can
// hand the content on without a second read.
func (kb *KB) graphViewLocked(read map[string]string) (*graphView, error) {
	c := kb.graphCacheLocked()

	files, err := kb.conceptFiles()
	if err != nil {
		return nil, err
	}
	c.stats.validations++
	c.stats.parsed, c.stats.reused = 0, 0
	changed := c.view == nil
	seen := make(map[string]struct{}, len(files))
	emitted := make([]*graphEntry, 0, len(files))

	for _, f := range files {
		seen[f.rel] = struct{}{}
		entry := kb.validateEntry(c, f, read)
		if entry == nil {
			// Unreadable: skipped, as the walk skips it.
			if _, had := c.entries[f.rel]; had {
				delete(c.entries, f.rel)
				changed = true
			}
			continue
		}
		if old := c.entries[f.rel]; old != entry {
			c.entries[f.rel] = entry
			if old == nil || old.hash != entry.hash || old.id != entry.id || !sameProbeResult(old, entry) {
				changed = true
			}
		}
		emitted = append(emitted, entry)
	}
	for rel := range c.entries {
		if _, ok := seen[rel]; !ok {
			delete(c.entries, rel)
			changed = true
		}
	}
	if !changed && c.view != nil && len(emitted) != len(c.view.entries) {
		changed = true
	}
	if !changed {
		return c.view, nil
	}
	c.view = buildGraphView(emitted, generationAfter(c.view))
	return c.view, nil
}

func generationAfter(v *graphView) uint64 {
	if v == nil {
		return 1
	}
	return v.generation + 1
}

// validateEntry returns the cache entry for f, reusing the cached one when its
// signature, its racy window and its asset probes all still hold, and
// re-reading the file otherwise. It returns nil for a file that cannot be read.
func (kb *KB) validateEntry(c *graphCache, f conceptFile, read map[string]string) *graphEntry {
	now := time.Now()
	old := c.entries[f.rel]
	// A symlinked concept is never cached: its lstat signature says nothing
	// about the target ReadRaw follows.
	symlink := f.entry.Type()&fs.ModeSymlink != 0
	var sig fileSig
	if info, err := f.entry.Info(); err == nil {
		sig = fileSig{size: info.Size(), modTime: info.ModTime().UnixNano(), mode: info.Mode()}
	} else {
		symlink = true // no signature to trust
	}
	if old != nil && !symlink && old.id == f.id && old.trusted(sig) && kb.probesHold(old.probes) {
		c.stats.reused++
		return old
	}

	content, err := kb.ReadRaw(f.rel)
	c.stats.reads++
	if err != nil {
		return nil
	}
	c.stats.parsed++
	if read != nil {
		read[f.rel] = content
	}
	hash := okf.ContentHash(content)
	if old != nil && old.id == f.id && old.hash == hash && kb.probesHold(old.probes) {
		// Same bytes: keep the parse, refresh what it was checked against.
		refreshed := *old
		refreshed.sig, refreshed.observed = sig, now
		if symlink {
			refreshed.sig = fileSig{modTime: now.UnixNano()} // never trusted
		}
		return &refreshed
	}
	entry := parseGraphEntry(kb, f, content, hash)
	entry.sig, entry.observed = sig, now
	if symlink {
		entry.sig = fileSig{modTime: now.UnixNano()}
	}
	return entry
}

func parseGraphEntry(kb *KB, f conceptFile, content, hash string) *graphEntry {
	_, body, _ := okf.SplitFrontmatter(content)
	probes := map[string]bool{}
	resolver := func(rel string) bool {
		ok := kb.AssetExists(rel)
		probes[rel] = ok
		return ok
	}
	return &graphEntry{
		id:     f.id,
		rel:    f.rel,
		links:  ExtractLinks(body, f.rel, resolver),
		probes: probes,
		hash:   hash,
		facets: facetsOf(content),
	}
}

func (kb *KB) probesHold(probes map[string]bool) bool {
	for rel, was := range probes {
		if kb.AssetExists(rel) != was {
			return false
		}
	}
	return true
}

// sameProbeResult reports whether two entries for the same bytes derived the
// same links (an asset appearing or vanishing changes them).
func sameProbeResult(a, b *graphEntry) bool {
	if len(a.links) != len(b.links) {
		return false
	}
	for i := range a.links {
		if a.links[i] != b.links[i] {
			return false
		}
	}
	return true
}

func buildGraphView(entries []*graphEntry, generation uint64) *graphView {
	v := &graphView{
		generation: generation,
		entries:    entries,
		adj: linkAdjacency{
			out: make(map[okf.ConceptID]map[okf.ConceptID]struct{}),
			in:  make(map[okf.ConceptID]map[okf.ConceptID]struct{}),
		},
		exists: make(map[okf.ConceptID]struct{}, len(entries)),
		facets: make(map[okf.ConceptID]ConceptFacets, len(entries)),
	}
	for _, e := range entries {
		v.exists[e.id] = struct{}{}
		v.facets[e.id] = e.facets
		if v.adj.out[e.id] == nil {
			v.adj.out[e.id] = make(map[okf.ConceptID]struct{})
		}
		for _, target := range e.links {
			v.adj.out[e.id][target] = struct{}{}
			if v.adj.in[target] == nil {
				v.adj.in[target] = make(map[okf.ConceptID]struct{})
			}
			v.adj.in[target][e.id] = struct{}{}
		}
	}
	return v
}

// ConceptFacets returns the frontmatter facets of one concept, resolving the
// file exactly as ReadConcept does (the direct form wins over an expanded
// index.md). It never runs a full validation — it is called once per concept
// inside visibility predicates — and re-reads the file only when its own
// signature is not trusted.
func (kb *KB) ConceptFacets(id okf.ConceptID) ConceptFacets {
	rel, _, err := kb.resolveConceptRelPath(id, false)
	if err != nil {
		return ConceptFacets{}
	}
	abs, err := kb.ResolvePath(rel, false)
	if err != nil {
		return ConceptFacets{}
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return ConceptFacets{}
	}
	symlink := info.Mode()&fs.ModeSymlink != 0
	sig := fileSig{size: info.Size(), modTime: info.ModTime().UnixNano(), mode: info.Mode()}

	kb.graphMu.Lock()
	defer kb.graphMu.Unlock()
	c := kb.graphCacheLocked()
	if !symlink {
		if e := c.entries[rel]; e != nil && e.trusted(sig) {
			return e.facets
		}
		if e := c.facetOnly[rel]; e != nil && e.trusted(sig) {
			return e.facets
		}
	}
	content, err := kb.ReadRaw(rel)
	c.stats.facetReads++
	c.stats.reads++
	if err != nil {
		delete(c.facetOnly, rel)
		return ConceptFacets{}
	}
	facets := facetsOf(content)
	if !symlink {
		c.facetOnly[rel] = &graphEntry{rel: rel, sig: sig, observed: time.Now(), facets: facets}
	}
	return facets
}

// ConceptChange is one id-level change a derived index must apply (D245).
type ConceptChange struct {
	ID okf.ConceptID
	// Content and Hash are the concept as ReadConcept resolves it; both are
	// empty for a removal.
	Content string
	Hash    string
	Removed bool
	// Known says the id had been reported before: an update, not an addition.
	Known bool
}

// ConceptChanges validates the cache and returns, in id order, every concept
// whose resolved content changed, appeared or vanished since the previous
// call (D245). This is how the search indexes follow the files: by
// validation, like the graph, not by each write path remembering to notify.
//
// An id resolves as ReadConcept resolves it — the direct form wins over an
// expanded index.md — because that is what the indexes hold. The content is
// the validation's own read; a second read happens only for an id whose file
// the validation reused (its content changed hands without the file changing,
// or a graph reader validated first). The first call reports every concept,
// unless SeedReportedHashes declared them.
func (kb *KB) ConceptChanges() ([]ConceptChange, error) {
	kb.graphMu.Lock()
	defer kb.graphMu.Unlock()
	read := map[string]string{}
	view, err := kb.graphViewLocked(read)
	if err != nil {
		return nil, err
	}
	c := kb.graph
	if c.reported == nil {
		c.reported = map[okf.ConceptID]string{}
	}

	resolved := make(map[okf.ConceptID]*graphEntry, len(view.entries))
	for _, e := range view.entries {
		direct := e.rel == string(e.id)+".md"
		if prev, ok := resolved[e.id]; ok && prev.rel == string(e.id)+".md" && !direct {
			continue
		}
		resolved[e.id] = e
	}

	var changes []ConceptChange
	for id, e := range resolved {
		old, known := c.reported[id]
		if known && old == e.hash {
			continue
		}
		content, ok := read[e.rel]
		hash := e.hash
		if !ok {
			raw, err := kb.ReadRaw(e.rel)
			c.stats.reads++
			if err != nil {
				continue // left unreported: the next call retries
			}
			content, hash = raw, okf.ContentHash(raw)
			if known && old == hash {
				continue
			}
		}
		c.reported[id] = hash
		changes = append(changes, ConceptChange{ID: id, Content: content, Hash: hash, Known: known})
	}
	for id := range c.reported {
		if _, ok := resolved[id]; !ok {
			delete(c.reported, id)
			changes = append(changes, ConceptChange{ID: id, Removed: true, Known: true})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].ID < changes[j].ID })
	return changes, nil
}

// SeedReportedHashes declares the content hashes a caller already holds, so
// the next ConceptChanges reports only what differs from them. A nil map
// makes the next call report every concept again.
func (kb *KB) SeedReportedHashes(hashes map[okf.ConceptID]string) {
	kb.graphMu.Lock()
	defer kb.graphMu.Unlock()
	c := kb.graphCacheLocked()
	if hashes == nil {
		c.reported = nil
		return
	}
	c.reported = make(map[okf.ConceptID]string, len(hashes))
	for id, h := range hashes {
		c.reported[id] = h
	}
}
