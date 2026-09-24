package mcpserver

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/graphalgo"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

const (
	atlasCentralCount   = 10
	atlasCommunityCount = 12
	atlasCommunityMin   = 3
)

// writeAtlasStructure appends atlas_overview's opt-in "## Structure" section
// (D244): the most central concepts by PageRank and the main communities, both
// computed on the graph of the concepts the caller can see, so the ranking
// discloses nothing about the others (D226).
func writeAtlasStructure(sb *strings.Builder, ctx requestContext, k *kb.KB) error {
	lg, err := k.LinkGraph(func(id string) bool { return Visible(ctx, k, id) })
	if err != nil {
		return err
	}
	g := lg.Graph
	name := func(i int) string {
		if t := lg.Facets[i].Title; t != "" {
			return fmt.Sprintf("%s (%s)", t, lg.IDs[i])
		}
		return string(lg.IDs[i])
	}

	sb.WriteString("\n## Structure\n\n### Most central\n\n")
	pr := graphalgo.PageRank(g, 0.85, 1e-9, 100)
	order := make([]int, g.N)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return pr[order[a]] > pr[order[b]] })
	if len(order) == 0 {
		sb.WriteString("No concepts.\n")
	}
	for _, i := range order[:min(atlasCentralCount, len(order))] {
		fmt.Fprintf(sb, "- %s\n", name(i))
	}

	degree := make([]int, g.N)
	for i := range degree {
		degree[i] = len(g.Out[i]) + len(g.In[i])
	}
	rank, list := graphalgo.Communities(g, 1, degree)
	members := make([][]int, len(list))
	for i, r := range rank {
		members[r] = append(members[r], i)
	}
	sb.WriteString("\n### Communities\n\n")
	written := 0
	for _, c := range list {
		if c.Size < atlasCommunityMin || written == atlasCommunityCount {
			break // ranked by size: every later one is smaller
		}
		// The dominant map is the plurality top-level collection; ties go to
		// the name first in order.
		count := map[string]int{}
		for _, i := range members[c.Rank] {
			count[lg.Facets[i].Collection]++
		}
		top := ""
		for m, n := range count {
			if n > count[top] || n == count[top] && m < top {
				top = m
			}
		}
		anchor := lg.Facets[c.Anchor].Title
		if anchor == "" {
			anchor = string(lg.IDs[c.Anchor])
		}
		share := int(math.Round(100 * float64(count[top]) / float64(c.Size)))
		fmt.Fprintf(sb, "- %s — %d concepts, mostly in %s (%d%%)\n", anchor, c.Size, top, share)
		written++
	}
	if written == 0 {
		sb.WriteString("No community of at least 3 concepts.\n")
	}
	return nil
}
