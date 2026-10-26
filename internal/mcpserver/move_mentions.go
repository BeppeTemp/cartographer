package mcpserver

import (
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// maxMentions caps the mentions a move reports; the rest is counted in
// `mentions_omitted` (D1070).
const maxMentions = 50

// mentionOut is one plain-text occurrence of a moved concept's old ID.
// Line is 1-based in the file, frontmatter included.
type mentionOut struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Line int    `json:"line"`
	Kind string `json:"kind"` // artifact | frontmatter | prose
}

// mentionConceptID is the concept ID of a concept file path.
func mentionConceptID(p string) string {
	p = strings.TrimSuffix(p, ".md")
	return strings.TrimSuffix(p, "/index")
}

// attachMentions sets result["mentions"] and result["mentions_omitted"].
// It only reads: it runs on the final state, after the links, the indexes and
// the log line are written, and nothing is written after it.
func attachMentions(k *kb.KB, result map[string]interface{}, moveMap map[string]string, view *callerView) {
	all := view.filterMentions(scanMentions(k, moveMap))
	if len(all) == 0 {
		return
	}
	if len(all) > maxMentions {
		result["mentions_omitted"] = len(all) - maxMentions
		all = all[:maxMentions]
	}
	result["mentions"] = all
}

// isIDByte reports a byte that continues a token: an old ID followed or
// preceded by one is another name, not a mention.
func isIDByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// scanMentions lists, sorted by path, line, id, every whole-token occurrence
// of an old ID (a key of moveMap) in the concepts and artifact text files.
//
// Traps, pinned by TestConceptMove_Mentions*:
//   - Whole token: the byte before is not [A-Za-z0-9_-] nor '/' (so
//     "dev/ops/net" does not mention "ops/net"), the byte after is not
//     [A-Za-z0-9_-]; a following ".md" or "." is fine.
//   - Links are excluded because the rewrite owns them: the targets of
//     markdown links and wiki-links are blanked with kb.MaskLinks, the very
//     recognition RewriteLinks uses. A link the rewrite could not follow
//     (rewrite_links=false) is in `findings`, not here.
//   - log.md, index.md and _map.md are not scanned (the log records the move,
//     indexes had their links rewritten, descriptors name maps).
func scanMentions(k *kb.KB, moveMap map[string]string) []mentionOut {
	oldIDs := make([]string, 0, len(moveMap))
	for o := range moveMap {
		oldIDs = append(oldIDs, o)
	}
	sort.Strings(oldIDs)

	var out []mentionOut
	scan := func(path, content string, artifact bool) {
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
				line := strings.Count(masked[:i], "\n") + 1
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
				out = append(out, mentionOut{ID: o, Path: path, Line: line, Kind: kind})
			}
		}
	}

	_ = k.WalkConceptPaths(func(_ okf.ConceptID, physicalPath, content string) error {
		scan(physicalPath, content, false)
		return nil
	})
	for _, f := range lint.ArtifactTextFiles(k) {
		scan(f.Path, f.Content, true)
	}

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
