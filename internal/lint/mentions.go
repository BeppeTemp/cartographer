package lint

import (
	"errors"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// Mention is one plain-text occurrence of an old concept ID. Line is 1-based in
// the file, frontmatter included; Kind is artifact | frontmatter | prose.
type Mention struct {
	ID   string
	Path string
	Line int
	Kind string
}

// MentionConceptID is the concept ID of a concept file path.
func MentionConceptID(p string) string {
	p = strings.TrimSuffix(p, ".md")
	return strings.TrimSuffix(p, "/index")
}

// isIDByte reports a byte that continues a token: an old ID followed or
// preceded by one is another name, not a mention.
func isIDByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// MentionFile is one file whose text ScanMentionFiles reads.
type MentionFile struct {
	Path     string
	Content  string
	Artifact bool
}

type mentionScanner struct {
	oldIDs  []string
	moveMap map[string]string
	out     []Mention
}

func newMentionScanner(moveMap map[string]string) *mentionScanner {
	oldIDs := make([]string, 0, len(moveMap))
	for o := range moveMap {
		oldIDs = append(oldIDs, o)
	}
	sort.Strings(oldIDs)
	return &mentionScanner{oldIDs: oldIDs, moveMap: moveMap}
}

func (sc *mentionScanner) sorted() []Mention {
	out := sc.out
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.ID < b.ID
	})
	return out
}

// ScanMentionFiles is ScanMentions over text the caller already holds: it
// reads no file, so a caller that has the pages in memory (kb_review) does not
// read the KB a second time.
func ScanMentionFiles(files []MentionFile, moveMap map[string]string) []Mention {
	sc := newMentionScanner(moveMap)
	for _, f := range files {
		sc.scan(f.Path, f.Content, f.Artifact)
	}
	return sc.sorted()
}

// ScanMentions lists, sorted by path, line, id, every whole-token occurrence
// of an old ID (a key of moveMap) in the concepts and artifact text files.
//
// Traps, pinned by the tests of this package and TestConceptMove_Mentions*:
//   - Whole token: the byte before is not [A-Za-z0-9_-] nor '/' (so
//     "dev/ops/net" does not mention "ops/net"), the byte after is not
//     [A-Za-z0-9_-]; a following ".md" or "." is fine.
//   - Links are excluded because the rewrite owns them: the targets of
//     markdown links and wiki-links are blanked with kb.MaskLinks, the very
//     recognition RewriteLinks uses. A link to an old ID is also what
//     broken_link reports, so it is never reported twice. A link the rewrite could not follow
//     (rewrite_links=false) is in `findings`, not here.
//   - log.md, index.md and _map.md are not scanned (the log records the move,
//     indexes had their links rewritten, descriptors name maps).
//
// A walk that fails part-way is returned with the mentions found before it
// (and in the artifacts): the list is then incomplete, and a caller must not
// read it as "no old ID left" (#1853).
func ScanMentions(k *kb.KB, moveMap map[string]string, candidates func(oldIDs []string) map[string]bool) ([]Mention, error) {
	sc := newMentionScanner(moveMap)
	walk := k.WalkConceptPaths
	if candidates != nil {
		if set := candidates(sc.oldIDs); set != nil {
			walk = func(fn func(okf.ConceptID, string, string) error) error {
				return k.WalkConceptPathsIn(set, fn)
			}
		}
	}
	return scanMentions(sc, walk, func() ([]ArtifactFile, error) { return ArtifactTextFilesErr(k) })
}

// scanMentions is ScanMentions over a concept walk and an artifact list, so a
// test can fail either.
func scanMentions(sc *mentionScanner, walk func(fn func(okf.ConceptID, string, string) error) error, artifacts func() ([]ArtifactFile, error)) ([]Mention, error) {
	walkErr := walk(func(_ okf.ConceptID, physicalPath, content string) error {
		sc.scan(physicalPath, content, false)
		return nil
	})
	files, artErr := artifacts()
	for _, f := range files {
		sc.scan(f.Path, f.Content, true)
	}
	return sc.sorted(), errors.Join(walkErr, artErr)
}

func (sc *mentionScanner) scan(path, content string, artifact bool) {
	oldIDs, moveMap := sc.oldIDs, sc.moveMap
	hit := false
	for _, o := range oldIDs {
		if strings.Contains(content, o) {
			hit = true
			break
		}
	}
	if !hit {
		return
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	fmLines := 0 // lines 1..fmLines are frontmatter, fences included
	if !artifact {
		if fm, _, ok := okf.SplitFrontmatter(content); ok {
			fmLines = strings.Count(fm, "\n") + 3
			if fm == "" {
				fmLines = 2
			}
		}
	}
	masked := kb.MaskLinks(content)
	for _, o := range oldIDs {
		newID := moveMap[o]
		seen := map[int]bool{}
		// Line numbers in one incremental pass: occurrences come in
		// increasing offset, so only the bytes since the last one are counted.
		line, counted := 1, 0
		for from := 0; ; {
			i := strings.Index(masked[from:], o)
			if i < 0 {
				break
			}
			i += from
			from = i + 1
			end := i + len(o)
			if i > 0 && (isIDByte(masked[i-1]) || masked[i-1] == '/') {
				continue
			}
			if end < len(masked) && isIDByte(masked[end]) {
				continue
			}
			if strings.HasPrefix(masked[i:], newID) { // the new ID, which contains the old
				continue
			}
			line += strings.Count(masked[counted:i], "\n")
			counted = i
			if seen[line] {
				continue
			}
			seen[line] = true
			kind := "prose"
			switch {
			case artifact:
				kind = "artifact"
			case line <= fmLines:
				kind = "frontmatter"
			}
			sc.out = append(sc.out, Mention{ID: o, Path: path, Line: line, Kind: kind})
		}
	}
}
