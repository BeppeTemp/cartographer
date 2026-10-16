package mcpserver

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// movedConcept is one applied move of applyConceptMoves, with every page it
// carried to a new ID.
type movedConcept struct {
	sourceID, targetID string
	// pages maps the old ID of each page to its new one: the concept itself
	// and, when it is expanded, every satellite under its directory.
	pages map[string]string
	// targetDir is the directory an expanded concept now lives in (absolute),
	// "" for a flat one.
	targetDir string
}

// rebaseMovedOutbound rewrites the relative links of every page a move
// carried, from the path the page had to the path it has (D160). It returns
// the number of links rewritten.
//
// A satellite is rebased like index.md: the directory moves as a unit, but a
// "../x.md" in it names a sibling of the OLD parent, and the new parent has
// other siblings. Pages that were not moved are never touched here.
//
// Trap: moveMap holds concept IDs only, so a link between two pages of the
// same expanded concept is not recognised as moving with them unless the
// concept's own files are mapped too: index.md ("<id>/index" is no concept ID)
// and every asset. Without that, the link is resolved against where its target
// used to be, and one that was still valid once the directory moved is broken.
func rebaseMovedOutbound(k *kb.KB, moved []movedConcept, moveMap map[string]string) (int, error) {
	outMap := make(map[string]string, len(moveMap))
	for oldID, newID := range moveMap {
		outMap[oldID] = newID
	}
	for _, mc := range moved {
		if mc.targetDir == "" {
			continue
		}
		err := filepath.WalkDir(mc.targetDir, func(abs string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(mc.targetDir, abs)
			if err != nil {
				return err
			}
			// A link to a concept resolves with its ".md" trimmed, one to
			// anything else keeps its name (the lookup in RewriteOutboundLinks).
			rel = strings.TrimSuffix(filepath.ToSlash(rel), ".md")
			if _, ok := outMap[mc.sourceID+"/"+rel]; !ok {
				outMap[mc.sourceID+"/"+rel] = mc.targetID + "/" + rel
			}
			return nil
		})
		if err != nil {
			return 0, fmt.Errorf("applied the move but could not list the files of %q: %w", mc.targetID, err)
		}
	}

	fixed := 0
	for _, mc := range moved {
		oldIDs := make([]string, 0, len(mc.pages))
		for oldID := range mc.pages {
			oldIDs = append(oldIDs, oldID)
		}
		sort.Strings(oldIDs)
		for _, oldID := range oldIDs {
			newID := mc.pages[oldID]
			// The new base is where the page is now (index.md for an
			// expanded concept); the old one is the same path under the old ID.
			newBase, _ := k.ConceptRelPath(okf.ConceptID(newID))
			oldBase := mc.sourceID + strings.TrimPrefix(newBase, mc.targetID)
			data, err := k.ReadConcept(okf.ConceptID(newID))
			if err != nil {
				continue
			}
			newBody, n := kb.RewriteOutboundLinks(data.Body, oldBase, newBase, outMap)
			if n == 0 {
				continue
			}
			fm, err := okf.ParseFrontmatter(data.FrontmatterRaw)
			if err != nil {
				continue
			}
			if _, err := k.WriteConcept(okf.ConceptID(newID), fm, newBody, data.ContentHash); err != nil {
				return fixed, fmt.Errorf("applied the move but could not rewrite %q's own links: %w", newID, err)
			}
			fixed += n
		}
	}
	return fixed, nil
}
