package kb

import (
	"path"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// recordedMovesMax bounds the history RecordedMoves reads: the last 200
// commits that contain a rename. Older moves are not seen.
const recordedMovesMax = 200

// Move is one concept ID that became another by a recorded move.
type Move struct{ From, To okf.ConceptID }

type movesCache struct {
	head    string
	renames []gitx.Rename // oldest first, at most recordedMovesMax commits
	moves   []Move
	// full and incremental count the git reads, for the tests: a write that
	// fast-forwards HEAD must cost the commits since the cached HEAD only.
	full, incremental int
}

// RecordedMoves returns the concept moves the KB's git history records, oldest
// first, from at most the last recordedMovesMax commits that contain a rename
// within the last gitx.MoveWalkCommits commits (shared and cached on the HEAD
// sha: callers must not modify the slice). A new HEAD that descends from the
// cached one costs only the commits since it; any other HEAD (a rewrite, a
// forced sync) reads the bounded history again.
//
// Traps: the subject of a move commit carries no pair (a batch concept_move has
// no source_id), so the pair is the git rename of the concept file in a commit
// whose subject is concept_move, concept_archive or kb_repair (gitx.MoveRenames).
// The limit counts rename commits, not commits. Rename detection is git's (-M,
// 50 % similarity): a page rewritten a lot in the move can read as delete + add
// and is missed (a silent false negative), and identical pages moved in one
// commit may be paired crosswise. concept_transfer, which moves a concept to
// another KB, is not a move here: its successor is not in this KB. A KB that is
// not a git repository has none.
func (kb *KB) RecordedMoves() ([]Move, error) {
	head, err := gitx.HeadSHA(kb.Root)
	if err != nil {
		return nil, nil // no repository or no commit: no history
	}
	kb.movesMu.Lock()
	defer kb.movesMu.Unlock()
	prev := kb.movesCache
	if prev != nil && prev.head == head {
		return prev.moves, nil
	}
	c := &movesCache{head: head}
	if prev != nil {
		c.full, c.incremental = prev.full, prev.incremental
	}
	var renames []gitx.Rename
	incremental := false
	if prev != nil {
		fresh, ok, err := gitx.MoveRenamesSince(kb.Root, prev.head, recordedMovesMax)
		if err != nil {
			return nil, err
		}
		if ok {
			incremental = true
			c.incremental++
			renames = append(append(renames, prev.renames...), fresh...)
		}
	}
	if !incremental {
		c.full++
		renames, err = gitx.MoveRenames(kb.Root, recordedMovesMax)
		if err != nil {
			return nil, err
		}
	}
	c.renames = lastCommits(renames, recordedMovesMax)
	for _, r := range c.renames {
		from, ok1 := conceptIDOfGitPath(r.OldPath)
		to, ok2 := conceptIDOfGitPath(r.Path)
		if !ok1 || !ok2 || from == to {
			continue
		}
		c.moves = append(c.moves, Move{From: from, To: to})
	}
	kb.movesCache = c
	return c.moves, nil
}

// lastCommits keeps the renames of the n newest commits (renames are oldest first).
func lastCommits(renames []gitx.Rename, n int) []gitx.Rename {
	seen, i := 0, len(renames)
	for i > 0 {
		if i == len(renames) || renames[i-1].SHA != renames[i].SHA {
			if seen == n {
				break
			}
			seen++
		}
		i--
	}
	return renames[i:]
}

// conceptIDOfGitPath is the concept ID of a git-relative path under data/, or
// false for anything that is not a concept file (a reserved file, an asset, a
// path outside data/, a name that is not a valid ID).
func conceptIDOfGitPath(p string) (okf.ConceptID, bool) {
	rest, ok := strings.CutPrefix(path.Clean(strings.ReplaceAll(p, "\\", "/")), "data/")
	if !ok || !strings.HasSuffix(rest, ".md") || IsReservedPath("data/"+rest) {
		return "", false
	}
	if id, expanded := GitPathToExpandedConceptID("data/" + rest); expanded {
		rest = id + ".md"
	}
	id, err := okf.PathToID(rest)
	if err != nil {
		return "", false
	}
	return id, true
}

// ResolveMoves maps each old ID that no longer exists to the ID a chain of
// recorded moves ends at, following the last move recorded for each source, at
// most maxHops times, to the first ID that exists. An old ID whose chain ends at
// nothing that exists (moved then deleted, a loop, too deep) is omitted, as is
// one that exists again; no ID maps to itself. Pure.
func ResolveMoves(moves []Move, exists func(okf.ConceptID) bool, maxHops int) map[okf.ConceptID]okf.ConceptID {
	next := make(map[okf.ConceptID]okf.ConceptID, len(moves))
	for _, m := range moves {
		next[m.From] = m.To // a later move of the same source overrides
	}
	out := map[okf.ConceptID]okf.ConceptID{}
	for from := range next {
		if exists(from) {
			continue
		}
		cur := from
		for i := 0; i < maxHops; i++ {
			n, ok := next[cur]
			if !ok {
				break
			}
			cur = n
			if exists(cur) {
				break
			}
		}
		if cur != from && exists(cur) {
			out[from] = cur
		}
	}
	return out
}
