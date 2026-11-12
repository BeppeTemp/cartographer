package kb

import (
	"crypto/sha256"

	"github.com/BeppeTemp/cartographer/internal/graphalgo"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// maxRankMemoEntries bounds the visible sets one generation keeps a ranking
// for: a handful of principals with different scopes, not one per request.
const maxRankMemoEntries = 8

// snapshotRank is what GraphSnapshot derives from the whole visible graph
// before scope and limit (D244): the PageRank per node, indexed like the
// sorted visible ids, and the communities.
type snapshotRank struct {
	pagerank    []float64
	communityOf []int
	communities []graphalgo.Community
	// members[r] lists the indexes of community r's members by PageRank, then
	// index (so the smallest id first on a tie): the cluster projection
	// (graphclusters.go) names and unfolds a community from it without
	// regrouping the graph on each request.
	members [][]int
}

// rankMemo holds the rankings of one graph view generation, by fingerprint of
// the visible id set. A new generation drops them all.
type rankMemo struct {
	generation uint64
	byVisible  map[[sha256.Size]byte]snapshotRank
}

// snapshotRank returns the ranking of the visible ids (sorted), computing it
// only when this generation has not seen this visible set. The result is
// never mutated by a caller.
func (kb *KB) snapshotRank(generation uint64, ids []okf.ConceptID, compute func() snapshotRank) snapshotRank {
	h := sha256.New()
	var sep [1]byte
	for _, id := range ids {
		h.Write([]byte(id))
		h.Write(sep[:]) // an id holds no NUL: the boundary keeps "ab","c" apart from "a","bc"
	}
	var key [sha256.Size]byte
	copy(key[:], h.Sum(nil))

	kb.rankMu.Lock()
	if m := kb.rankMemo; m != nil && m.generation == generation {
		if r, ok := m.byVisible[key]; ok {
			kb.rankMu.Unlock()
			return r
		}
	}
	kb.rankMu.Unlock()

	r := compute()

	kb.rankMu.Lock()
	defer kb.rankMu.Unlock()
	kb.rankRuns++
	m := kb.rankMemo
	if m == nil || m.generation != generation {
		m = &rankMemo{generation: generation, byVisible: map[[sha256.Size]byte]snapshotRank{}}
		kb.rankMemo = m
	}
	if len(m.byVisible) >= maxRankMemoEntries {
		m.byVisible = map[[sha256.Size]byte]snapshotRank{}
	}
	m.byVisible[key] = r
	return r
}

// GraphRankComputations reports how many times a snapshot had to compute its
// PageRank and communities rather than reuse them: a test asserts that a
// repeated request, or one differing only in scope or limit, computes none.
func (kb *KB) GraphRankComputations() int {
	kb.rankMu.Lock()
	defer kb.rankMu.Unlock()
	return kb.rankRuns
}
