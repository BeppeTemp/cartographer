package lint

import (
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

const (
	// movedIDHops is how many recorded moves a chain is followed through.
	movedIDHops = 16
	// movedIDMax caps the old IDs scanned for: the newest in the history.
	movedIDMax = 100
	// movedIDPairsShown caps the old → new pairs a message spells out.
	movedIDPairsShown = 5
)

// recordedMoves is kb.RecordedMoves, a variable so a test can fail it.
var recordedMoves = (*kb.KB).RecordedMoves

// movedHistoryWarn logs an unreadable history once per process.
var movedHistoryWarn sync.Once

// movedIDCitations is the moved_id_cited observation: the pages (concepts and
// artifact text files) that still name, outside a link, an ID that a move
// recorded in the git history renamed (kb.RecordedMoves), one carrier per file.
// An old ID whose chain ends at nothing that exists is not reported (there is
// nothing to suggest; a link to it is broken_link), and links are not scanned
// (ScanMentions masks them): this is never a duplicate of broken_link. No
// missing old ID means no scan of the KB.
//
// concepts are the pages Review already holds (path, full text): the scan reads
// no concept from disk a second time. Artifact files are read here, and only
// when an old ID is missing.
func movedIDCitations(k *kb.KB, exists map[okf.ConceptID]struct{}, concepts []MentionFile) ([]Finding, error) {
	moves, err := recordedMoves(k)
	if err != nil {
		// A history that cannot be read costs this observation, not the review.
		movedHistoryWarn.Do(func() {
			log.Printf("kb_review: moved_id_cited skipped, the git history of the KB cannot be read: %v", err)
		})
		return nil, nil
	}
	if len(moves) == 0 {
		return nil, nil
	}
	resolved := kb.ResolveMoves(moves, func(id okf.ConceptID) bool {
		_, ok := exists[id]
		return ok
	}, movedIDHops)
	if len(resolved) == 0 {
		return nil, nil
	}
	// The newest old IDs, by the position of their last move in the history.
	pos := make(map[okf.ConceptID]int, len(resolved))
	for i, m := range moves {
		if _, ok := resolved[m.From]; ok {
			pos[m.From] = i
		}
	}
	olds := make([]okf.ConceptID, 0, len(pos))
	for id := range pos {
		olds = append(olds, id)
	}
	sort.Slice(olds, func(i, j int) bool { return pos[olds[i]] > pos[olds[j]] })
	if len(olds) > movedIDMax {
		olds = olds[:movedIDMax]
	}
	moveMap := make(map[string]string, len(olds))
	for _, o := range olds {
		moveMap[string(o)] = string(resolved[o])
	}

	type cite struct {
		lines    []int
		artifact bool
	}
	byPath := map[string]map[string]*cite{} // path -> old ID -> lines
	files := append([]MentionFile(nil), concepts...)
	for _, f := range ArtifactTextFiles(k) {
		files = append(files, MentionFile{Path: f.Path, Content: f.Content, Artifact: true})
	}
	for _, m := range ScanMentionFiles(files, moveMap) {
		if byPath[m.Path] == nil {
			byPath[m.Path] = map[string]*cite{}
		}
		c := byPath[m.Path][m.ID]
		if c == nil {
			c = &cite{artifact: m.Kind == "artifact"}
			byPath[m.Path][m.ID] = c
		}
		c.lines = append(c.lines, m.Line)
	}
	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var out []Finding
	for _, p := range paths {
		ids := make([]string, 0, len(byPath[p]))
		artifact := false
		for id, c := range byPath[p] {
			ids = append(ids, id)
			artifact = artifact || c.artifact
		}
		sort.Strings(ids)
		var pairs []string
		for i, id := range ids {
			if i == movedIDPairsShown {
				pairs = append(pairs, "and "+strconv.Itoa(len(ids)-movedIDPairsShown)+" more")
				break
			}
			lines := make([]string, 0, len(byPath[p][id].lines))
			for _, l := range byPath[p][id].lines {
				lines = append(lines, strconv.Itoa(l))
			}
			pairs = append(pairs, fmt.Sprintf("cites %s (line %s) — now %s", id, strings.Join(lines, ", "), moveMap[id]))
		}
		f := Finding{Path: p, Artifact: artifact, Message: strings.Join(pairs, "; ")}
		if !artifact {
			f.Path = MentionConceptID(p)
		}
		out = append(out, newObservation("moved_id_cited", f))
	}
	return out, nil
}
