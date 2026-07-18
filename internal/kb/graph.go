package kb

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

var mdLinkRe = regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)

// wikiLinkRe matches wiki-links [[id]] and [[id#section]]. Alias form
// [[id|text]] is deliberately not matched here (the "|text" part would fail
// the character class below, so it falls through unrecognized).
var wikiLinkRe = regexp.MustCompile(`\[\[([^\[\]|#]+)(#[^\[\]]*)?\]\]`)

// ExtractLinks parses markdown links and wiki-links from the body of a
// concept and returns the referenced concept IDs. Absolute URLs and anchors
// are skipped. basePath is the concept's own path relative to the KB root
// (e.g. "arch/dossier/concept.md").
//
// Markdown links [text](path.md) are resolved relative to basePath. Wiki-
// links [[id]] and [[id#section]] are root-relative: the ID is taken as-is
// (path from the KB root, without .md). The alias form [[id|text]] is not
// supported and is not extracted. Both syntaxes dedup against the same seen
// set.
func ExtractLinks(body string, basePath string) []okf.ConceptID {
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

		if !strings.HasSuffix(href, ".md") {
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
		id, frag := sub[1], sub[2]
		newID, ok := moveMap[id]
		if !ok {
			return match
		}
		count++
		return "[[" + newID + frag + "]]"
	})

	return body, count
}

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

// GraphNeighbors returns the concept IDs reachable from id within depth hops.
// The returned map is conceptID → minimum distance from the starting concept.
// The starting concept itself is not included. depth <= 0 defaults to 1.
func (kb *KB) GraphNeighbors(id okf.ConceptID, depth int) (map[string]int, error) {
	if depth <= 0 {
		depth = 1
	}

	result := map[string]int{}
	frontier := []string{string(id)}

	for d := 1; d <= depth; d++ {
		var next []string
		for _, cur := range frontier {
			relPath := okf.IDToPath(okf.ConceptID(cur))
			content, err := kb.ReadRaw(relPath)
			if err != nil {
				continue
			}
			_, body, _ := okf.SplitFrontmatter(content)
			links := ExtractLinks(body, relPath)
			for _, link := range links {
				lid := string(link)
				if lid == string(id) {
					continue
				}
				if _, seen := result[lid]; !seen {
					result[lid] = d
					next = append(next, lid)
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
	files, err := kb.listMDFiles(".")
	if err != nil {
		return err
	}
	svcFiles, err := kb.listServiceFiles()
	if err != nil {
		return err
	}
	files = append(files, svcFiles...)

	for _, rel := range files {
		rel = filepath.ToSlash(rel)
		base := path.Base(rel)

		if base == "index.md" {
			dir := path.Dir(rel)
			if dir == "." || len(strings.Split(dir, "/")) != 2 {
				// Root index.md and map-level index.md ("map/index.md")
				// stay reserved/excluded — only an expanded concept's
				// index.md ("map/concept/index.md") is emitted.
				continue
			}
			content, err := kb.ReadRaw(rel)
			if err != nil {
				continue
			}
			if err := fn(okf.ConceptID(dir), content); err != nil {
				return err
			}
			continue
		}

		if okf.IsReserved(base) {
			continue
		}
		content, err := kb.ReadRaw(rel)
		if err != nil {
			continue
		}
		id := okf.ConceptID(strings.TrimSuffix(rel, ".md"))
		if err := fn(id, content); err != nil {
			return err
		}
	}
	return nil
}

// listServiceFiles lists .md files under services/ (relative to Root, e.g.
// "services/keycloak.md"). These paths are resolvable via ReadRaw because
// ResolvePath anchors the services/ tree at Root. Returns nil if services/
// does not exist.
func (kb *KB) listServiceFiles() ([]string, error) {
	servicesDir := filepath.Join(kb.Root, "services")
	if _, err := os.Stat(servicesDir); os.IsNotExist(err) {
		return nil, nil
	}
	var files []string
	err := filepath.WalkDir(servicesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			rel, _ := filepath.Rel(kb.Root, p)
			files = append(files, rel)
		}
		return nil
	})
	return files, err
}
