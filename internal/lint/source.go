package lint

import (
	"fmt"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// FirstMachinePath returns the first client-local path in s as the machine_path
// check sees it (URLs excluded, no allow-prefixes), or "".
func FirstMachinePath(s string) string { return firstDisallowedMachinePath(s, nil) }

// uncitedSourceFinding implements source_uncited (D278): a Source marked
// ingested that no concept cites through provenance was marked done while its
// content never reached the KB. cited is kb.SourceCitations of the whole KB.
func uncitedSourceFinding(relPath string, id okf.ConceptID, content string, cited map[string][]string) (Finding, bool) {
	fmRaw, _, _ := okf.SplitFrontmatter(content)
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil || fm.Type() != kb.SourceType {
		return Finding{}, false
	}
	if v, _ := fm.Get("ingest_status"); v != "ingested" || len(cited[string(id)]) > 0 {
		return Finding{}, false
	}
	return newFinding("source_uncited", Finding{
		Path:    relPath,
		Message: fmt.Sprintf("source %s is marked ingested but no concept lists it in provenance — cite it from the pages built on it, or set ingest_status back to pending", id),
	}), true
}
