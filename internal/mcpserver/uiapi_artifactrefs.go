package mcpserver

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// Which concepts an artifact reads: the explicit references in its files — a
// [[wiki-link]] or a concept id written out — resolved against the concepts
// that exist. A concept naming a skill in prose is not a reference: a name
// like "detox" is also a word, so the relation is taken only from the side
// that spells out an id, never guessed from names.

var (
	artifactWikiRef = regexp.MustCompile(`\[\[([^\]|#]+)`)
	// A path-shaped token: what a concept id looks like when written out.
	artifactIDToken = regexp.MustCompile(`[\p{L}\p{N}_][\p{L}\p{N}_.-]*(?:/[\p{L}\p{N}_][\p{L}\p{N}_.-]*)+`)
)

// artifactConceptRefs returns the existing concepts an artifact references,
// sorted and deduplicated.
func artifactConceptRefs(a kbArtifact, exists map[okf.ConceptID]struct{}) []string {
	seen := map[string]bool{}
	add := func(raw string) {
		id := strings.TrimSpace(raw)
		id = strings.TrimSuffix(strings.TrimSuffix(id, ".md"), "/index")
		id = strings.TrimPrefix(id, "/")
		if _, ok := exists[okf.ConceptID(id)]; ok {
			seen[id] = true
		}
	}
	for _, f := range a.Files {
		if !utf8.Valid(f.Content) {
			continue
		}
		text := string(f.Content)
		for _, m := range artifactWikiRef.FindAllStringSubmatch(text, -1) {
			add(m[1])
		}
		for _, tok := range artifactIDToken.FindAllString(text, -1) {
			add(strings.TrimRight(tok, "."))
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// conceptIDSet is the set of concept ids, read once per request.
func conceptIDSet(k *kb.KB) (map[okf.ConceptID]struct{}, error) {
	links, err := k.Links()
	if err != nil {
		return nil, err
	}
	return links.Exists, nil
}

// uiArtifactRef names an artifact from a concept's side.
type uiArtifactRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// artifactsReading lists the skills, agents and hooks that reference a
// concept. Only for a principal that sees the whole KB (artifacts are
// whole-KB resources); nil otherwise.
func artifactsReading(ctx requestContext, srv *Server, id string) []uiArtifactRef {
	if srv == nil || srv.kbRef == nil || !WholeVisible(ctx, srv.kbRef, false) {
		return nil
	}
	exists, err := conceptIDSet(srv.kbRef)
	if err != nil {
		return nil
	}
	catalog, err := listKBArtifacts(srv.kbRef, srv.kbArtifacts.allowlist, srv.kbArtifacts.signer)
	if err != nil {
		return nil
	}
	out := []uiArtifactRef{}
	for _, a := range catalog.Artifacts {
		if a.Kind == "template" || a.Kind == "instructions" {
			continue
		}
		for _, ref := range artifactConceptRefs(a, exists) {
			if ref == id {
				out = append(out, uiArtifactRef{Kind: a.Kind, Name: a.Name})
				break
			}
		}
	}
	return out
}
