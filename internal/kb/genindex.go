package kb

import (
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/blocktext"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// Generated map and journal indexes (D301). A map whose contract says
// `index: generated` has its concept list kept by the server inside a marked
// block of its index.md; every byte outside the block is the operator's.

// IndexBlockBegin and IndexBlockEnd delimit the generated block. The begin
// marker is matched by its stable prefix (up to " — "), as blocktext does.
const (
	IndexBlockBegin = "<!-- cartographer:index begin — generated from this map's concepts; edit outside the block -->"
	IndexBlockEnd   = "<!-- cartographer:index end -->"
)

// journalMonthGroupMin is the entry count above which a journal's generated
// index is grouped by month: a short journal reads better as one list.
const journalMonthGroupMin = 30

// ConceptSummary is what the graph cache knows about one concept without
// reading its body: identity, two frontmatter facets and the file size.
type ConceptSummary struct {
	ID       okf.ConceptID
	Title    string
	Type     string
	Status   string
	Bytes    int64
	Expanded bool
}

// ConceptSummaries returns every concept of the KB, sorted by ID, from the
// stat-validated graph cache (D241): no body is read on a warm cache. When
// two files emit one ID the later in walk order wins, as in GraphSnapshot.
func (kb *KB) ConceptSummaries() ([]ConceptSummary, error) {
	view, err := kb.graphView()
	if err != nil {
		return nil, err
	}
	byID := make(map[okf.ConceptID]ConceptSummary, len(view.entries))
	for _, e := range view.entries {
		byID[e.id] = ConceptSummary{
			ID:       e.id,
			Title:    e.facets.Title,
			Type:     e.facets.Type,
			Status:   e.facets.Status,
			Bytes:    e.sig.size,
			Expanded: e.rel == path.Join(string(e.id), "index.md"),
		}
	}
	out := make([]ConceptSummary, 0, len(byID))
	for _, s := range byID {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// RenderIndexBlock renders the generated block (markers included, ending in a
// newline) for a map's direct concepts. kind is the map's descriptor kind:
// "journal" lists newest ID first, grouped by month past
// journalMonthGroupMin entries; anything else groups by type when there are
// at least two, sorted by title then ID. The same entries always render the
// same bytes.
func RenderIndexBlock(kind string, entries []ConceptSummary) string {
	var b strings.Builder
	b.WriteString(IndexBlockBegin + "\n")
	line := func(e ConceptSummary) {
		title := strings.Join(strings.Fields(e.Title), " ")
		if title == "" {
			title = string(e.ID)
		}
		fmt.Fprintf(&b, "- [[%s]] — %s\n", e.ID, title)
	}
	es := append([]ConceptSummary(nil), entries...)
	if kind == "journal" {
		sort.Slice(es, func(i, j int) bool { return es[i].ID > es[j].ID })
		if len(es) <= journalMonthGroupMin {
			for _, e := range es {
				line(e)
			}
		} else {
			// Dated entries first, newest month first; undated ones last.
			var undated []ConceptSummary
			month := ""
			for _, e := range es {
				m, ok := journalMonth(e.ID)
				if !ok {
					undated = append(undated, e)
					continue
				}
				if m != month {
					month = m
					fmt.Fprintf(&b, "### %s\n", m)
				}
				line(e)
			}
			if len(undated) > 0 {
				b.WriteString("### Undated\n")
				for _, e := range undated {
					line(e)
				}
			}
		}
	} else {
		sort.Slice(es, func(i, j int) bool {
			if es[i].Title != es[j].Title {
				return es[i].Title < es[j].Title
			}
			return es[i].ID < es[j].ID
		})
		groups := map[string][]ConceptSummary{}
		var types []string
		for _, e := range es {
			if _, ok := groups[e.Type]; !ok {
				types = append(types, e.Type)
			}
			groups[e.Type] = append(groups[e.Type], e)
		}
		if len(types) < 2 {
			for _, e := range es {
				line(e)
			}
		} else {
			// Typed groups alphabetically; untyped concepts last.
			sort.Slice(types, func(i, j int) bool {
				if (types[i] == "") != (types[j] == "") {
					return types[j] == ""
				}
				return types[i] < types[j]
			})
			for _, t := range types {
				heading := t
				if heading == "" {
					heading = "Other"
				}
				fmt.Fprintf(&b, "### %s\n", heading)
				for _, e := range groups[t] {
					line(e)
				}
			}
		}
	}
	b.WriteString(IndexBlockEnd + "\n")
	return b.String()
}

// journalMonth returns the YYYY-MM prefix of a journal entry's own segment.
func journalMonth(id okf.ConceptID) (string, bool) {
	s := string(id)
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if len(s) < 7 || s[4] != '-' {
		return "", false
	}
	for _, i := range []int{0, 1, 2, 3, 5, 6} {
		if s[i] < '0' || s[i] > '9' {
			return "", false
		}
	}
	return s[:7], true
}

// IndexBlock returns the generated block of content (markers included, plus
// the newline after the end marker when present) and whether there is one.
func IndexBlock(content string) (string, bool) {
	stable := IndexBlockBegin[:strings.Index(IndexBlockBegin, " — ")]
	start := strings.Index(content, stable)
	if start < 0 {
		return "", false
	}
	n := strings.Index(content[start:], IndexBlockEnd)
	if n < 0 {
		return "", false
	}
	end := start + n + len(IndexBlockEnd)
	if end < len(content) && content[end] == '\n' {
		end++
	}
	return content[start:end], true
}

// withIndexBlock returns content with its generated block replaced by block,
// or block appended after the curated text when there is none yet.
func withIndexBlock(content, block string) string {
	if replaced, ok := blocktext.ReplaceBetween(content, IndexBlockBegin, IndexBlockEnd, block); ok {
		return replaced
	}
	trimmed := strings.TrimRight(content, "\n")
	if trimmed == "" {
		return block
	}
	return trimmed + "\n\n" + block
}

// directConcepts filters summaries to a map's direct concepts: two-segment
// IDs under name. An expanded concept is one entry; its satellites are not.
func directConcepts(name string, all []ConceptSummary) []ConceptSummary {
	var out []ConceptSummary
	prefix := name + "/"
	for _, s := range all {
		rest, ok := strings.CutPrefix(string(s.ID), prefix)
		if ok && rest != "" && !strings.Contains(rest, "/") {
			out = append(out, s)
		}
	}
	return out
}

// ExpectedIndexBlock is the block a generated map's index.md should carry now.
func (kb *KB) ExpectedIndexBlock(name string, contract MapContract) (string, error) {
	all, err := kb.ConceptSummaries()
	if err != nil {
		return "", err
	}
	return RenderIndexBlock(contract.Kind, directConcepts(name, all)), nil
}

// RegenerateIndexes rewrites the generated block of every map whose contract
// says `index: generated`, only where the bytes differ, and returns the maps
// it wrote. It runs after every successful write (gitWrap), so an index edited
// out of band heals on the next write. A map whose index cannot be resolved
// safely (a symlink on the path) is skipped with the error joined in.
func (kb *KB) RegenerateIndexes() ([]string, error) {
	archives, err := kb.ListArchives()
	if err != nil {
		return nil, err
	}
	var all []ConceptSummary
	var written []string
	var errs []error
	for _, name := range archives {
		contract, cerr := kb.ReadMapContract(name)
		if cerr != nil || contract.Index != IndexGenerated {
			continue
		}
		if all == nil {
			if all, err = kb.ConceptSummaries(); err != nil {
				return nil, err
			}
		}
		block := RenderIndexBlock(contract.Kind, directConcepts(name, all))
		changed, werr := kb.writeIndexBlock(name, block)
		if werr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, werr))
			continue
		}
		if changed {
			written = append(written, name)
		}
	}
	return written, errors.Join(errs...)
}

// writeIndexBlock installs block in a map's index.md through the same
// symlink-checked resolution and atomic writer as PatchIndex.
func (kb *KB) writeIndexBlock(name, block string) (bool, error) {
	relPath, err := kb.curatedIndexRelPath(name)
	if err != nil {
		return false, err
	}
	absPath, err := kb.ResolvePath(relPath, true)
	if err != nil {
		return false, err
	}
	current := ""
	data, err := os.ReadFile(absPath)
	switch {
	case err == nil:
		current = string(data)
	case !os.IsNotExist(err):
		return false, err
	}
	next := withIndexBlock(current, block)
	if next == current {
		return false, nil
	}
	if err := writeFileAtomic(absPath, []byte(next)); err != nil {
		return false, err
	}
	return true, nil
}
