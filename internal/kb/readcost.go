package kb

import (
	"sort"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// ByteStats are nearest-rank percentiles of a set of byte counts.
type ByteStats struct {
	P50 int64 `json:"p50"`
	P90 int64 `json:"p90"`
	Max int64 `json:"max"`
}

// ReadCost is what reading the KB costs an agent (D301): the size of one
// concept, and of a concept plus its in- and out-neighbours (each counted
// once), the neighbourhood an agent keeping related pages consistent reads.
type ReadCost struct {
	ConceptBytes       ByteStats `json:"concept_bytes"`
	NeighbourhoodBytes ByteStats `json:"neighbourhood_bytes"`
}

// ReadCost computes the read-cost percentiles over the concepts include
// accepts (nil accepts all), on the graph those concepts induce: a hidden
// concept is neither counted nor a neighbour, so the numbers are the ones a
// KB without it would give (D226). Sizes come from the graph cache's stat
// signatures; no body is read.
//
// Archived concepts (D322) are left out: a closed, harvested entry is not
// something an agent reads to work, so its size must not inflate the
// numbers. Only archived: deprecated and superseded concepts have always
// counted and stay, being few and still meaningful.
func (kb *KB) ReadCost(include func(id string) bool) (ReadCost, error) {
	live := func(id string) bool {
		return (include == nil || include(id)) && kb.ConceptFacets(okf.ConceptID(id)).Status != StatusArchived
	}
	lg, err := kb.LinkGraph(live)
	if err != nil {
		return ReadCost{}, err
	}
	n := len(lg.IDs)
	own := make([]int64, n)
	hood := make([]int64, n)
	for i := 0; i < n; i++ {
		own[i] = lg.Facets[i].Bytes
		seen := map[int]bool{i: true}
		total := own[i]
		for _, list := range [][]int{lg.Graph.Out[i], lg.Graph.In[i]} {
			for _, j := range list {
				if !seen[j] {
					seen[j] = true
					total += lg.Facets[j].Bytes
				}
			}
		}
		hood[i] = total
	}
	return ReadCost{ConceptBytes: byteStats(own), NeighbourhoodBytes: byteStats(hood)}, nil
}

func byteStats(v []int64) ByteStats {
	if len(v) == 0 {
		return ByteStats{}
	}
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	rank := func(p int) int64 {
		i := (p*len(v)+99)/100 - 1
		if i < 0 {
			i = 0
		}
		return v[i]
	}
	return ByteStats{P50: rank(50), P90: rank(90), Max: v[len(v)-1]}
}
