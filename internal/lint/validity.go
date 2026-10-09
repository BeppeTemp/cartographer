package lint

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/search"
)

// Fix kinds of the invalid-page checks (D356).
const (
	// FixAddFrontmatter: the page has no frontmatter. Field = "type", To = the
	// type every sibling already uses; the title is derived when the fix is
	// applied (DeriveTitle), so it is not carried.
	FixAddFrontmatter = "add_frontmatter"
	// FixQuoteValue: Field = the key whose scalar value is double-quoted so the
	// block parses (QuoteBrokenValue).
	FixQuoteValue = "quote_value"
	// FixMove: Field = the concept ID, To = the ID it moves to, applied through
	// concept_move (nonslug_file_name).
	FixMove = "move"
)

// slugPattern is the concept file-name pattern, the same as an artifact slug.
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-{1,2}[a-z0-9]+)*$`)

// mapTypes is the type a map's pages agree on, per map (D356 decision 3), or ""
// when they do not. Computed once per run from the walk.
type mapTypes map[string]string

// newMapTypes resolves, for every map of concepts, the single type used by all
// the typed pages of the map when at least two agree. Pages without a type are
// the ones being repaired: they are not votes, and not disagreement either.
// Why not the most common type: a majority is a guess, and a wrong type
// silently changes contracts. The map contract's default_template type is the
// first source once the map default_template exists; until then the contract has no such field.
func newMapTypes(concepts map[okf.ConceptID]string) mapTypes {
	type tally struct {
		typ   string
		n     int
		mixed bool
	}
	by := map[string]*tally{}
	for id, content := range concepts {
		mapName, _, ok := strings.Cut(string(id), "/")
		if !ok {
			continue
		}
		raw, _, hasFM := okf.SplitFrontmatter(content)
		if !hasFM {
			continue
		}
		parsed, err := okf.ParseFrontmatter(raw)
		if err != nil || parsed.Type() == "" {
			continue
		}
		t := by[mapName]
		if t == nil {
			by[mapName] = &tally{typ: parsed.Type(), n: 1}
			continue
		}
		t.n++
		if t.typ != parsed.Type() {
			t.mixed = true
		}
	}
	out := mapTypes{}
	for name, t := range by {
		if !t.mixed && t.n >= 2 {
			out[name] = t.typ
		}
	}
	return out
}

// of returns the resolved type for a concept of mapName, or "".
func (m mapTypes) of(id okf.ConceptID) string {
	mapName, _, _ := strings.Cut(string(id), "/")
	return m[mapName]
}

// validityFindings reports what the write path would refuse about an existing
// page (D356): the validation errors, as lint findings, so the repair and the
// doctor can see them; and the two page-shape warnings. content is the whole
// file.
func validityFindings(id okf.ConceptID, content string, types mapTypes, slugTargetFree func(okf.ConceptID) bool) []Finding {
	var out []Finding
	path := okf.IDToPath(id)
	raw, body, hasFM := okf.SplitFrontmatter(content)
	typ := types.of(id)

	switch {
	case !hasFM:
		f := Finding{Path: path, Message: "missing or unparseable frontmatter: the page has no frontmatter block"}
		if typ != "" {
			f.Fix = &Fix{Kind: FixAddFrontmatter, Field: "type", To: typ}
		} else {
			f.Message += " — no type resolves for it (the map's other pages do not all share one), so add the frontmatter by hand"
		}
		out = append(out, newFinding("missing_frontmatter", f))
	default:
		parsed, err := okf.ParseFrontmatter(raw)
		switch {
		case err != nil:
			f := Finding{Path: path, Message: "unparseable frontmatter: " + err.Error()}
			if _, key, ok := QuoteBrokenValue(raw); ok {
				f.Fix = &Fix{Kind: FixQuoteValue, Field: key}
			} else {
				f.Message += " — more than one line is affected, so fix the block by hand"
			}
			out = append(out, newFinding("unparseable_frontmatter", f))
		case parsed.Type() == "":
			f := Finding{Path: path, Message: "type field is required"}
			if typ != "" {
				f.Fix = &Fix{Kind: FixSetValue, Field: "type", To: typ}
			} else {
				f.Message += " — no type resolves for it (the map's other pages do not all share one), so set it by hand"
			}
			out = append(out, newFinding("missing_type", f))
		}
	}

	if !kb.IsServicesID(id) {
		if n := len(strings.Split(string(id), "/")); n > kb.MaxConceptDepth {
			out = append(out, newFinding("concept_too_deep", Finding{
				Path:    path,
				Message: fmt.Sprintf("concept depth (%d segments) exceeds the max of %d (map/concept/child): a write to it is refused — where it belongs is a judgement, move it with concept_move", n, kb.MaxConceptDepth),
			}))
		}
	}

	if strings.TrimSpace(body) == "" {
		out = append(out, newFinding("empty_concept", Finding{
			Path:    path,
			Message: "the page has no body (only frontmatter, or an empty file) — write it, or retire it; the server never deletes it",
		}))
	}

	if stem := idBase(id); !slugPattern.MatchString(stem) {
		f := Finding{Path: path, Message: fmt.Sprintf("file name %q is not a lowercase-hyphenated slug (a-z, 0-9, -)", stem)}
		if slug := Slugify(stem); slug != "" {
			target := okf.ConceptID(strings.TrimSuffix(string(id), stem) + slug)
			if slugTargetFree(target) {
				f.Fix = &Fix{Kind: FixMove, Field: string(id), To: string(target)}
			} else {
				f.Message += fmt.Sprintf("; %q would be its slug but that concept already exists, so rename it by hand", string(target))
			}
		}
		out = append(out, newFinding("nonslug_file_name", f))
	}
	return out
}

// Slugify turns a file stem into the slug form: lowercase, accents folded, every
// run of other characters one hyphen, no leading or trailing hyphen.
func Slugify(s string) string {
	var sb strings.Builder
	pendingHyphen := false
	for _, r := range search.Fold(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if pendingHyphen && sb.Len() > 0 {
				sb.WriteByte('-')
			}
			pendingHyphen = false
			sb.WriteRune(r)
			continue
		}
		pendingHyphen = true
	}
	return sb.String()
}

// DeriveTitle is the title add_frontmatter gives a page: its first H1, else its
// file stem with '-' and '_' turned into spaces. Derived, never invented.
func DeriveTitle(body string, id okf.ConceptID) string {
	if t := firstH1(body); t != "" {
		return t
	}
	return strings.Join(strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(idBase(id))), " ")
}

var failedLineRe = regexp.MustCompile(`\(line (\d+)\)`)

// QuoteBrokenValue repairs an unparseable frontmatter block when the answer is
// unique: exactly one line fails (the parser names it), the lines after it are
// plain top-level entries (so the value was never meant to span lines), and
// double-quoting that line's value makes the whole block parse with the same
// key set. It returns the repaired block and the key it quoted; ok is false
// otherwise, and nothing is guessed.
func QuoteBrokenValue(raw string) (fixed, key string, ok bool) {
	_, err := okf.ParseFrontmatter(raw)
	if err == nil {
		return "", "", false
	}
	m := failedLineRe.FindStringSubmatch(err.Error())
	if m == nil {
		return "", "", false
	}
	n, _ := strconv.Atoi(m[1])
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	if n < 1 || n > len(lines) {
		return "", "", false
	}
	bad := lines[n-1]
	colon := strings.Index(bad, ":")
	if colon < 0 || bad[0] == ' ' || bad[0] == '\t' {
		return "", "", false
	}
	// A value that goes on over the next lines is a multi-line block: quoting
	// its first line would cut it.
	emptyKey := false
	for _, l := range lines[n:] {
		t := strings.TrimSpace(l)
		switch {
		case t == "" || strings.HasPrefix(t, "#"):
		case l[0] == ' ' || l[0] == '\t' || strings.HasPrefix(t, "- "):
			if !emptyKey {
				return "", "", false
			}
		case strings.Contains(l, ":"):
			emptyKey = strings.TrimSpace(l[strings.Index(l, ":")+1:]) == ""
		default:
			return "", "", false
		}
	}
	key = strings.TrimSpace(bad[:colon])
	value := strings.TrimSpace(bad[colon+1:])
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
	lines[n-1] = bad[:colon+1] + ` "` + escaped + `"`
	fixed = strings.Join(lines, "\n")
	parsed, perr := okf.ParseFrontmatter(fixed)
	if perr != nil {
		return "", "", false
	}
	// Same key set: one entry per top-level `key:` line, none swallowed.
	want := map[string]bool{}
	for _, l := range lines {
		if l == "" || l[0] == ' ' || l[0] == '\t' || l[0] == '#' || strings.HasPrefix(l, "- ") {
			continue
		}
		if i := strings.Index(l, ":"); i >= 0 {
			want[strings.TrimSpace(l[:i])] = true
		}
	}
	got := parsed.Keys()
	if len(got) != len(want) {
		return "", "", false
	}
	for _, g := range got {
		if !want[g] {
			return "", "", false
		}
	}
	return fixed, key, true
}

// idBase is the last segment of a concept ID: the file stem, or the directory
// name of an expanded concept.
func idBase(id okf.ConceptID) string {
	s := string(id)
	return s[strings.LastIndex(s, "/")+1:]
}
