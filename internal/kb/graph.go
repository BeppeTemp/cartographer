package kb

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

var mdLinkRe = regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)

// wikiLinkRe matches wiki-links [[id]], [[id#section]] and the alias forms
// [[id|text]] / [[id#section|text]] (D150). The ID is capture group 1: whatever
// precedes the first "|" or "#". The label is deliberately not returned —
// ExtractLinks yields ConceptIDs and no caller needs the text — but it must be
// matched, because the alias form is the only way to keep a readable label on a
// wiki-link, and a wiki-link is the only base-independent form (D149).
var wikiLinkRe = regexp.MustCompile(`\[\[([^\[\]|#]+)(#[^\[\]|]*)?(\|[^\[\]]*)?\]\]`)

// fenceRe matches an opening or closing code fence: three or more backticks or
// tildes at the start of a line, after optional indentation.
var fenceRe = regexp.MustCompile("^[ \t]*(`{3,}|~{3,})")

// inlineCodeRe matches an inline code span delimited by matched runs of
// backticks on one line.
var inlineCodeRe = regexp.MustCompile("(`+)[^`\n]*?(`+)")

// maskCodeSpans blanks every byte inside a fenced block or an inline code span,
// keeping newlines and total length identical so byte offsets stay valid for
// any other span-based reasoning over the same body (lint's machine_path check
// already works that way).
//
// Without this, two constructs that are normal in any KB documenting commands or
// drawing diagrams became link targets: a Mermaid subroutine node
// (N1[["a label"]] inside a mermaid block) and a POSIX character class
// (grep -E "x[[:alpha:]]"), plus any markdown link shown as an example. In the
// field, every surviving broken_link finding had this root cause (D150).
//
// Indented (four-space) code blocks are deliberately NOT treated as code: they
// are indistinguishable from a continuation line inside a list, which is how
// most KB bodies indent, so masking them would hide real links.
func maskCodeSpans(body string) string {
	lines := strings.Split(body, "\n")
	var fence string // the open fence's delimiter run, empty when outside
	blank := func(i int) { lines[i] = strings.Repeat(" ", len(lines[i])) }

	for i, line := range lines {
		m := fenceRe.FindStringSubmatch(line)
		if fence == "" {
			if m != nil {
				fence = m[1]
				blank(i) // a fence line carries no link
				continue
			}
			// Outside a fence: mask inline spans only.
			lines[i] = inlineCodeRe.ReplaceAllStringFunc(line, func(s string) string {
				return strings.Repeat(" ", len(s))
			})
			continue
		}
		// Inside a fence: everything is masked, and a closing fence of the same
		// character and at least the same length ends the block.
		closes := m != nil && m[1][0] == fence[0] && len(m[1]) >= len(fence)
		blank(i)
		if closes {
			fence = ""
		}
	}
	return strings.Join(lines, "\n")
}

// ExtractLinks parses markdown links and wiki-links from the body of a
// concept and returns the referenced concept IDs. Absolute URLs and anchors
// are skipped. basePath is the concept's own path relative to the KB root
// (e.g. "arch/dossier/concept.md").
//
// Markdown links [text](path.md) are resolved relative to basePath. Wiki-
// links [[id]], [[id#section]] and their alias forms [[id|text]] are
// root-relative: the ID is taken as-is (path from the KB root, without .md).
// Fenced blocks and inline code spans are not scanned (D150). Both syntaxes
// dedup against the same seen set.
// assetResolver, when supplied, reports whether a data-root-relative path is an
// existing asset. Variadic so every existing caller and test keeps compiling and
// keeps today's behaviour: with no resolver, an extensionless href stays a
// ConceptID shorthand.
func ExtractLinks(body string, basePath string, assetResolver ...func(relPath string) bool) []okf.ConceptID {
	var isAsset func(string) bool
	if len(assetResolver) > 0 {
		isAsset = assetResolver[0]
	}
	body = maskCodeSpans(body)
	baseDir := path.Dir(basePath)
	seen := map[string]bool{}
	var ids []okf.ConceptID

	addID := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, okf.ConceptID(id))
	}

	for _, m := range mdLinkRe.FindAllStringSubmatch(body, -1) {
		href := m[2]
		if strings.Contains(href, "://") || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") {
			continue
		}

		href = strings.SplitN(href, "#", 2)[0]
		if href == "" {
			continue
		}

		// A path with a non-Markdown extension is a file/asset link, not a
		// shorthand ConceptID. In particular, report.csv must not become the
		// false graph target report.csv.md.
		if ext := path.Ext(href); ext != "" && !strings.EqualFold(ext, ".md") {
			continue
		}
		if !strings.EqualFold(path.Ext(href), ".md") {
			// An extensionless href may cite an extensionless ASSET of the
			// citing concept — a Dockerfile, a Makefile, a LICENSE (D150).
			// Appending .md unconditionally turned those into links to
			// nonexistent concepts and left the asset orphan_asset forever,
			// while renaming the file to satisfy the linter would be worse than
			// the finding. Scoped to the concept's own asset set, so a stray
			// file elsewhere in the KB cannot absorb a shorthand ConceptID.
			if isAsset != nil && isAsset(path.Clean(path.Join(baseDir, href))) {
				continue
			}
			href += ".md"
		}

		resolved := path.Clean(path.Join(baseDir, href))
		if strings.HasPrefix(resolved, "..") {
			continue
		}

		addID(strings.TrimSuffix(resolved, ".md"))
	}

	for _, m := range wikiLinkRe.FindAllStringSubmatch(body, -1) {
		id := m[1]
		if id == "" || strings.Contains(id, "://") {
			continue
		}
		addID(strings.TrimSuffix(id, ".md"))
	}

	return ids
}

// ExtractAssetLinks returns the physical KB-relative targets of Markdown
// links that name a non-Markdown file. It deliberately does not turn those
// paths into ConceptIDs. basePath is the actual source file path, so links in
// an expanded owner's index.md resolve from the owner directory.
func ExtractAssetLinks(body string, basePath string, assetResolver ...func(relPath string) bool) []string {
	var isAsset func(string) bool
	if len(assetResolver) > 0 {
		isAsset = assetResolver[0]
	}
	baseDir := path.Dir(basePath)
	seen := map[string]bool{}
	var links []string
	for _, m := range mdLinkRe.FindAllStringSubmatch(body, -1) {
		href := m[2]
		if strings.Contains(href, "://") || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "/") {
			continue
		}
		if idx := strings.IndexAny(href, "?#"); idx >= 0 {
			href = href[:idx]
		}
		if href == "" || strings.EqualFold(path.Ext(href), ".md") {
			continue
		}
		resolved := path.Clean(path.Join(baseDir, href))
		// An extensionless href counts as an asset citation only when it really
		// resolves to one — a Dockerfile, a Makefile, a LICENSE (D150).
		// Otherwise it is a ConceptID shorthand and not this function's business.
		if path.Ext(href) == "" && (isAsset == nil || !isAsset(resolved)) {
			continue
		}
		if strings.HasPrefix(resolved, "..") || seen[resolved] {
			continue
		}
		seen[resolved] = true
		links = append(links, resolved)
	}
	return links
}

// RewriteLinks rewrites, in body, every markdown link and wiki-link whose
// resolved target concept ID is a key in moveMap (old ID → new ID), and
// returns the updated body plus the number of replacements performed.
// basePath is the linking concept's own current path relative to the KB root
// (same meaning and resolution rules as ExtractLinks' basePath): markdown
// hrefs are resolved relative to path.Dir(basePath) and rewritten to the new
// relative path (from the same directory) to the moved target, preserving
// any "#fragment" and adding back the ".md" suffix. Wiki-links are
// root-relative and are rewritten by simple ID substitution, preserving any
// "#section" suffix. Links whose resolved target is not in moveMap are left
// untouched.
func RewriteLinks(body string, basePath string, moveMap map[string]string) (string, int) {
	if len(moveMap) == 0 {
		return body, 0
	}

	baseDir := path.Dir(basePath)
	count := 0

	body = mdLinkRe.ReplaceAllStringFunc(body, func(match string) string {
		sub := mdLinkRe.FindStringSubmatch(match)
		text, href := sub[1], sub[2]
		if strings.Contains(href, "://") || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") {
			return match
		}

		pathPart, frag := href, ""
		if idx := strings.Index(href, "#"); idx >= 0 {
			pathPart, frag = href[:idx], href[idx:]
		}
		if pathPart == "" {
			return match
		}

		pathPartMd := pathPart
		if !strings.HasSuffix(pathPartMd, ".md") {
			pathPartMd += ".md"
		}

		resolved := path.Clean(path.Join(baseDir, pathPartMd))
		if strings.HasPrefix(resolved, "..") {
			return match
		}

		targetID := strings.TrimSuffix(resolved, ".md")
		newID, ok := moveMap[targetID]
		if !ok {
			return match
		}

		count++
		newHref := relLink(baseDir, newID+".md") + frag
		return "[" + text + "](" + newHref + ")"
	})

	body = wikiLinkRe.ReplaceAllStringFunc(body, func(match string) string {
		sub := wikiLinkRe.FindStringSubmatch(match)
		// sub[3] is the "|label" segment (D150): it is preserved verbatim, so a
		// rename never costs the human-readable label.
		id, frag, label := sub[1], sub[2], sub[3]
		newID, ok := moveMap[id]
		if !ok {
			return match
		}
		count++
		return "[[" + newID + frag + label + "]]"
	})

	return body, count
}

// RewriteOutboundLinks rebases every relative markdown link in body from oldBase
// to newBase, both being the linking concept's own path relative to the KB root
// (the same meaning as ExtractLinks' basePath). Returns the updated body and the
// number of replacements.
//
// concept_move rewrote inbound links only, so the moved concept's own relative
// links broke whenever the directory depth changed — documented, and still a
// half-move (D160). The delta is known at move time, so this is arithmetic, not
// guesswork.
//
// moveMap, when non-empty, is consulted first so a batch of moves composes: a
// link whose target is itself being moved resolves to the target's new location
// rather than to where it used to sit. Wiki-links are root-relative and
// deliberately untouched; absolute URLs, anchors and mailto: are skipped, and a
// link resolving outside the KB is left alone.
func RewriteOutboundLinks(body, oldBase, newBase string, moveMap map[string]string) (string, int) {
	oldDir, newDir := path.Dir(oldBase), path.Dir(newBase)
	if oldDir == newDir && len(moveMap) == 0 {
		return body, 0
	}
	count := 0
	out := mdLinkRe.ReplaceAllStringFunc(body, func(match string) string {
		sub := mdLinkRe.FindStringSubmatch(match)
		text, href := sub[1], sub[2]
		if strings.Contains(href, "://") || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "/") {
			return match
		}
		pathPart, frag := href, ""
		if idx := strings.Index(href, "#"); idx >= 0 {
			pathPart, frag = href[:idx], href[idx:]
		}
		if pathPart == "" {
			return match
		}
		resolved := path.Clean(path.Join(oldDir, pathPart))
		if strings.HasPrefix(resolved, "..") {
			return match
		}
		// A target moved in the same batch lands at its new path.
		target := resolved
		if moved, ok := moveMap[strings.TrimSuffix(resolved, ".md")]; ok {
			target = moved + ".md"
		}
		newHref := RelLink(newDir, target)
		if !strings.EqualFold(path.Ext(pathPart), ".md") {
			newHref = strings.TrimSuffix(newHref, ".md")
		}
		if newHref+frag == href {
			return match
		}
		count++
		return "[" + text + "](" + newHref + frag + ")"
	})
	return out, count
}

// RelLink is relLink exported (D160): concept_move needs it to rewrite the moved
// concept's own outbound links, and cmd/cartographer/import.go had a private copy
// whose comment said it existed "to avoid exporting KB-internal move machinery for
// a single caller". There are now two callers, so the copy is gone.
func RelLink(baseDir, targetPath string) string { return relLink(baseDir, targetPath) }

// relLink returns the relative path (forward-slash, markdown-link style)
// from directory baseDir to file targetPath, both expressed as clean
// forward-slash paths relative to the KB root (baseDir "." means the root).
func relLink(baseDir, targetPath string) string {
	baseDir = path.Clean(baseDir)
	var baseParts []string
	if baseDir != "." {
		baseParts = strings.Split(baseDir, "/")
	}
	targetParts := strings.Split(path.Clean(targetPath), "/")

	i := 0
	for i < len(baseParts) && i < len(targetParts)-1 && baseParts[i] == targetParts[i] {
		i++
	}

	var relParts []string
	for j := i; j < len(baseParts); j++ {
		relParts = append(relParts, "..")
	}
	relParts = append(relParts, targetParts[i:]...)
	return strings.Join(relParts, "/")
}

// linkAdjacency is the disposable directed graph derived from concept files.
// It deliberately retains links to missing concepts and self-edges: lint needs
// both facts. Graph traversal filters self-edges separately.
type linkAdjacency struct {
	out map[okf.ConceptID]map[okf.ConceptID]struct{}
	in  map[okf.ConceptID]map[okf.ConceptID]struct{}
}

// AssetExists reports whether relPath (from the data root) is an existing
// regular file that is not a concept — i.e. an asset. Used as ExtractLinks'
// asset resolver (D150); os.Lstat, never os.Stat, so a symlinked path is not an
// asset (same rule as asset.go).
func (kb *KB) AssetExists(relPath string) bool {
	if strings.EqualFold(path.Ext(relPath), ".md") {
		return false
	}
	abs, err := kb.ResolvePath(relPath, false)
	if err != nil {
		return false
	}
	info, err := os.Lstat(abs)
	return err == nil && info.Mode().IsRegular()
}

// buildLinkAdjacency returns the link graph of the current cached view
// (D241): validated against the files on every call, so it always follows
// them, but re-parsing only what changed. The maps belong to an immutable view
// and must not be mutated.
func (kb *KB) buildLinkAdjacency() (linkAdjacency, error) {
	view, err := kb.graphView()
	if err != nil {
		return linkAdjacency{}, err
	}
	return view.adj, nil
}

// Links is the whole link graph at one moment, as maps: both directions and
// the set of ids that are concepts. It belongs to an immutable cached view
// (D241): callers read it freely and must never mutate it. LinkGraph is the
// int-indexed projection the graph algorithms run on.
type Links struct {
	Out    map[okf.ConceptID]map[okf.ConceptID]struct{}
	In     map[okf.ConceptID]map[okf.ConceptID]struct{}
	Exists map[okf.ConceptID]struct{}
}

// Links validates the cache against the files once and returns the current
// graph, for a caller that needs several lookups to agree with each other and
// not to re-validate per lookup.
func (kb *KB) Links() (Links, error) {
	view, err := kb.graphView()
	if err != nil {
		return Links{}, err
	}
	return Links{Out: view.adj.out, In: view.adj.in, Exists: view.exists}, nil
}

// IncomingLinks returns the derived inbound links keyed by target concept.
// The graph is validated against the KB files on every call (D241). The
// returned map is shared and must not be mutated.
func (kb *KB) IncomingLinks() (map[okf.ConceptID]map[okf.ConceptID]struct{}, error) {
	graph, err := kb.buildLinkAdjacency()
	if err != nil {
		return nil, err
	}
	return graph.in, nil
}

// GraphNeighbors returns the concept IDs reachable from id within depth hops.
// The optional direction is out (default), in, or both. The returned map is
// conceptID → minimum distance from the starting concept. The starting concept
// and self-edges are not included. depth <= 0 defaults to 1.
func (kb *KB) GraphNeighbors(id okf.ConceptID, depth int, directions ...string) (map[string]int, error) {
	graph, err := kb.Links()
	if err != nil {
		return nil, err
	}
	return graph.Neighbors(id, depth, directions...)
}

// Neighbors is GraphNeighbors over this graph, for a caller that already
// holds one and needs its other lookups to agree with the traversal.
func (g Links) Neighbors(id okf.ConceptID, depth int, directions ...string) (map[string]int, error) {
	if depth <= 0 {
		depth = 1
	}
	direction := "out"
	if len(directions) > 0 && directions[0] != "" {
		direction = directions[0]
	}
	if direction != "out" && direction != "in" && direction != "both" {
		return nil, fmt.Errorf("invalid graph direction %q", direction)
	}

	result := map[string]int{}
	frontier := []okf.ConceptID{id}

	for d := 1; d <= depth; d++ {
		var next []okf.ConceptID
		for _, cur := range frontier {
			neighbors := make(map[okf.ConceptID]struct{})
			if direction == "out" || direction == "both" {
				for neighbor := range g.Out[cur] {
					neighbors[neighbor] = struct{}{}
				}
			}
			if direction == "in" || direction == "both" {
				for neighbor := range g.In[cur] {
					neighbors[neighbor] = struct{}{}
				}
			}
			for neighbor := range neighbors {
				if neighbor == cur || neighbor == id {
					continue
				}
				if _, seen := result[string(neighbor)]; !seen {
					result[string(neighbor)] = d
					next = append(next, neighbor)
				}
			}
		}
		frontier = next
	}

	return result, nil
}

// WalkConcepts calls fn for every non-reserved .md file in the KB, plus every
// expanded concept's index.md (D77 WP2): an index.md at exactly two path
// segments deep — e.g. "map/concept/index.md" — is the expanded form of the
// concept "map/concept" (see ExpandConcept) and is emitted with that ID.
// index.md at the root or at one segment deep (map-level, e.g.
// "map/index.md") stays reserved/excluded, same as before.
//
// It walks the data/ conceptual tree plus the services/ tree (a sibling of
// data/ under Root whose type:Service concepts must participate in search,
// graph, lint and service_list). Other reserved files (log.md, _map.md,
// _archive.md, AGENTS.md) are always skipped. raw/ is outside both roots.
func (kb *KB) WalkConcepts(fn func(id okf.ConceptID, content string) error) error {
	return kb.walkConceptPaths(func(id okf.ConceptID, _ string, content string) error {
		return fn(id, content)
	})
}

// WalkConceptPaths is WalkConcepts' physical-path-aware variant: fn also
// receives the actual KB-relative Markdown path holding the concept, which is
// what tells an expanded concept ("map/concept/index.md") from a flat one
// without a second stat per concept.
func (kb *KB) WalkConceptPaths(fn func(id okf.ConceptID, physicalPath, content string) error) error {
	return kb.walkConceptPaths(fn)
}

// walkConceptPaths is WalkConcepts' internal physical-path-aware variant.
// physicalPath is always the actual KB-relative Markdown path for id.
func (kb *KB) walkConceptPaths(fn func(id okf.ConceptID, physicalPath, content string) error) error {
	files, err := kb.conceptFiles()
	if err != nil {
		return err
	}
	for _, f := range files {
		content, err := kb.ReadRaw(f.rel)
		if err != nil {
			continue
		}
		if err := fn(f.id, f.rel, content); err != nil {
			return err
		}
	}
	return nil
}
