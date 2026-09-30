package kb

import (
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// SourceType is the conventional concept type of a source-ledger entry (D278).
const SourceType = "Source"

// SourceIngestStatuses is the vocabulary of a Source's ingest_status.
var SourceIngestStatuses = []string{"pending", "ingested", "skipped"}

// ProvenanceEntries returns the trimmed, non-empty entries of a concept's
// provenance frontmatter. A bare string is read as a one-element list. An entry
// that is the ID of an existing Source is a source citation (D278); the caller
// decides that, this only parses.
func ProvenanceEntries(fm *okf.Frontmatter) []string {
	if fm == nil {
		return nil
	}
	v, ok := fm.Get("provenance")
	if !ok {
		return nil
	}
	var raw []string
	switch t := v.(type) {
	case string:
		raw = []string{t}
	case []string:
		raw = t
	}
	var out []string
	for _, e := range raw {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// SourceCitations returns, for every concept in content, the provenance entries
// it lists, keyed by the cited ID: cited[sourceID] = citing concept IDs. Whether
// a key is a Source is for the caller to decide.
func SourceCitations(all map[okf.ConceptID]string) map[string][]string {
	out := map[string][]string{}
	for id, content := range all {
		fmRaw, _, _ := okf.SplitFrontmatter(content)
		fm, err := okf.ParseFrontmatter(fmRaw)
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, e := range ProvenanceEntries(fm) {
			if !seen[e] && e != string(id) {
				seen[e] = true
				out[e] = append(out[e], string(id))
			}
		}
	}
	return out
}
