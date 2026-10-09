package lint

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// The drift audit's checks (D357): shapes a KB drifts into that nothing
// reported. unknown_type and value_case_variant need KB-wide data (the type
// palette, the spellings in use), computed once per run in newDriftData;
// unmapped_folder and stray_file read the data/ tree.

// driftData is the KB-wide input of unknown_type and value_case_variant.
type driftData struct {
	// palette is the known concept types: those the KB's templates declare
	// plus the concept_types of its strict maps. Empty means no palette, and
	// unknown_type is silent.
	palette map[string]bool
	// statuses counts each spelling of the status field over every concept.
	statuses map[string]int
}

func newDriftData(k *kb.KB, concepts map[okf.ConceptID]string, contracts map[string]kb.MapContract) driftData {
	d := driftData{palette: map[string]bool{}, statuses: map[string]int{}}
	for _, t := range k.TemplateTypes() {
		d.palette[t] = true
	}
	for _, c := range contracts {
		if c.OntologyMode != "strict" {
			continue
		}
		for _, t := range c.ConceptTypes {
			if t = strings.TrimSpace(t); t != "" {
				d.palette[t] = true
			}
		}
	}
	for _, content := range concepts {
		raw, _, ok := okf.SplitFrontmatter(content)
		if !ok {
			continue
		}
		fm, err := okf.ParseFrontmatter(raw)
		if err != nil {
			continue
		}
		if s := statusSpelling(fm); s != "" {
			d.statuses[s]++
		}
	}
	return d
}

// statusSpelling is a concept's status as written, or "" when it has none or it
// is not a plain string.
func statusSpelling(fm *okf.Frontmatter) string {
	v, _ := fm.Get("status")
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// conceptFindings are the findings of the two per-concept checks.
func (d driftData) conceptFindings(relPath string, fm *okf.Frontmatter, contract *kb.MapContract) []Finding {
	if fm == nil {
		return nil
	}
	var out []Finding

	// --- unknown_type (warning, D357) ---
	if typ := fm.Type(); typ != "" && len(d.palette) > 0 && !d.palette[typ] && !strings.HasPrefix(typ, "{{") {
		f := Finding{Path: relPath}
		var match []string
		for known := range d.palette {
			if strings.EqualFold(known, typ) {
				match = append(match, known)
			}
		}
		known := make([]string, 0, len(d.palette))
		for t := range d.palette {
			known = append(known, t)
		}
		sort.Strings(known)
		f.Message = fmt.Sprintf("type %q is not one the KB declares (templates and strict maps: %s)", typ, strings.Join(known, ", "))
		if len(match) == 1 {
			f.Fix = &Fix{Kind: FixSetValue, Field: "type", To: match[0]}
			f.Message += fmt.Sprintf(" — fix: set it to %q", match[0])
		}
		out = append(out, newFinding("unknown_type", f))
	}

	// --- value_case_variant (warning, D357) ---
	// status in a map whose contract does not constrain it: a constrained
	// field belongs to invalid_field_value.
	if s := statusSpelling(fm); s != "" {
		if contract != nil {
			if _, constrained := contract.AllowedValues(fm.Type(), "status"); constrained {
				return out
			}
		}
		if f, ok := d.caseVariant(relPath, s); ok {
			out = append(out, f)
		}
	}
	return out
}

// caseVariant reports a status spelled differently, case-folded, from one more
// concepts use. The fix goes to the majority spelling; a tie has none.
func (d driftData) caseVariant(relPath, spelling string) (Finding, bool) {
	var variants []string
	for s := range d.statuses {
		if strings.EqualFold(s, spelling) {
			variants = append(variants, s)
		}
	}
	if len(variants) < 2 {
		return Finding{}, false
	}
	sort.Strings(variants)
	top, topN, tied := "", 0, false
	for _, s := range variants {
		switch n := d.statuses[s]; {
		case n > topN:
			top, topN, tied = s, n, false
		case n == topN:
			tied = true
		}
	}
	if !tied && spelling == top {
		return Finding{}, false
	}
	f := Finding{Path: relPath}
	if tied {
		f.Message = fmt.Sprintf("status %q is also spelled %s elsewhere, equally often: pick one spelling", spelling, quoteList(without(variants, spelling)))
	} else {
		f.Message = fmt.Sprintf("status %q is spelled %q by more concepts — fix: set it to %q", spelling, top, top)
		f.Fix = &Fix{Kind: FixSetValue, Field: "status", To: top}
	}
	return newFinding("value_case_variant", f), true
}

func without(list []string, drop string) []string {
	var out []string
	for _, s := range list {
		if s != drop {
			out = append(out, s)
		}
	}
	return out
}

func quoteList(list []string) string {
	q := make([]string, len(list))
	for i, s := range list {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, ", ")
}

// unmappedFolderFindings reports the data/ folders that hold concepts and no
// _map.md (or legacy _archive.md): map_list shows them with no title or kind,
// and no contract applies. Only a top-level folder is a map; a directory inside
// an expanded concept is not reported.
func unmappedFolderFindings(k *kb.KB, archives []string, concepts map[okf.ConceptID]string, scopeMatches func(string) bool) []Finding {
	withConcepts := map[string]bool{}
	for id := range concepts {
		if m, _, ok := strings.Cut(string(id), "/"); ok {
			withConcepts[m] = true
		}
	}
	var out []Finding
	for _, name := range archives {
		if !withConcepts[name] || !scopeMatches(name) {
			continue
		}
		if _, err := k.ReadRaw(name + "/_map.md"); err == nil {
			continue
		}
		if _, err := k.ReadRaw(name + "/_archive.md"); err == nil {
			continue // legacy_archive_descriptor owns it
		}
		out = append(out, newFinding("unmapped_folder", Finding{
			Path:    name,
			Message: fmt.Sprintf("folder %q holds concepts but has no _map.md: map_list shows it with no title or kind and no contract applies — fix: scaffold the descriptor", name),
			Fix:     &Fix{Kind: FixScaffoldMap, Field: name, To: HumanizeName(name)},
		}))
	}
	return out
}

// strayFileFindings reports the non-Markdown files in data/ that are not an
// asset: at the top of data/ or directly in a map. Anything deeper belongs to
// an expanded concept, whose assets orphan_asset and junk_asset cover; junk and
// dot files are other checks' (junk_file) or the server's own.
func strayFileFindings(k *kb.KB, scope string) []Finding {
	root := k.DataRoot()
	var out []Finding
	_ = filepath.WalkDir(root, func(abs string, d fs.DirEntry, err error) error {
		if err != nil || abs == root {
			return nil
		}
		rel, rerr := filepath.Rel(root, abs)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		depth := strings.Count(rel, "/") + 1
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if depth >= 2 {
				return filepath.SkipDir // an expanded concept: its files are assets
			}
			return nil
		}
		if !d.Type().IsRegular() || strings.EqualFold(filepath.Ext(rel), ".md") || kb.IsJunkPath(rel) {
			return nil
		}
		if scope != "" && rel != scope && !strings.HasPrefix(rel, scope+"/") {
			return nil
		}
		out = append(out, newFinding("stray_file", Finding{
			Path:    rel,
			Message: fmt.Sprintf("%s is not Markdown and not an asset of an expanded concept: move it out of data/, or give it an owner (concept_expand + asset_write); deleting it is your call", rel),
		}))
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
