package lint

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// ReviewIndexUnclassified (D1078) is a curated map index that still holds
// entries under "## New pages", where concept_new(index_entry) and a creating
// concept_batch park a page (D714) and nothing asks anyone to file it later.
const ReviewIndexUnclassified = "index_unclassified"

// indexUnclassifiedListMax caps the concepts an item names; the evidence
// carries the total.
const indexUnclassifiedListMax = 20

// newPagesEntries returns the concept ids linked under the kb.NewPagesHeading
// of an index body: the lines after the heading up to the next heading of the
// same or higher level (H1 or H2), code masked, links read by kb.ExtractLinks.
func newPagesEntries(k *kb.KB, mapName, body string, exists map[okf.ConceptID]struct{}) []string {
	var section []string
	in := false
	for _, line := range strings.Split(kb.MaskCodeSpans(body), "\n") {
		t := strings.TrimSpace(line)
		if !in {
			in = t == kb.NewPagesHeading
			continue
		}
		if strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "## ") {
			break
		}
		section = append(section, line)
	}
	seen := map[string]bool{}
	var ids []string
	for _, id := range kb.ExtractLinks(strings.Join(section, "\n"), mapName+"/index.md", k.AssetExists) {
		if _, ok := exists[id]; ok && !seen[string(id)] {
			seen[string(id)] = true
			ids = append(ids, string(id))
		}
	}
	sort.Strings(ids)
	return ids
}

// indexUnclassifiedItems lists, per map with a curated index (archives are
// visited in sorted order), the entries still under "## New pages": one item
// per index. Only that heading: "## Moved here" is another shape and stays out.
func indexUnclassifiedItems(k *kb.KB, contracts map[string]kb.MapContract, archives []string, exists map[okf.ConceptID]struct{}) []ReviewItem {
	names := append([]string(nil), archives...)
	sort.Strings(names)
	var out []ReviewItem
	for _, name := range names {
		if c, ok := contracts[name]; ok && c.Index == kb.IndexGenerated {
			continue
		}
		content, err := k.ReadIndex(name)
		if err != nil {
			continue
		}
		_, body, _ := okf.SplitFrontmatter(content)
		ids := newPagesEntries(k, name, body, exists)
		if len(ids) == 0 {
			continue
		}
		total := len(ids)
		out = append(out, ReviewItem{
			Kind:            ReviewIndexUnclassified,
			Concepts:        capIndexIDs(ids),
			Evidence:        indexUnclassifiedEvidence(name, total),
			SuggestedAction: "move each entry under the section of the index it belongs to (index_get, then index_patch), or drop the entry if the page needs none; remove the heading when it is empty",
			Weight:          total,
			indexMap:        name,
			indexIDs:        ids,
		})
	}
	return out
}

// capIndexIDs keeps the first indexUnclassifiedListMax ids.
func capIndexIDs(ids []string) []string {
	if len(ids) > indexUnclassifiedListMax {
		return ids[:indexUnclassifiedListMax]
	}
	return ids
}

// indexUnclassifiedEvidence is the item's evidence; FilterReview rebuilds it
// with the count of the entries the caller can see.
func indexUnclassifiedEvidence(mapName string, total int) string {
	noun := "entries"
	if total == 1 {
		noun = "entry"
	}
	return fmt.Sprintf("%d %s of %s/index.md still under %q", total, noun, mapName, kb.NewPagesHeading)
}
