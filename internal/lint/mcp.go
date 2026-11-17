package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// checkMCP reports every mcp/<name>.json that artifact_write would now refuse
// (a missing or unsupported `type`, a missing url, a literal secret...), so a
// KB written before that validation, or edited by git, surfaces its broken
// descriptors. BuildManifest leaves such a descriptor out of the manifest with
// an issue naming it and serves the rest of the KB.
//
// A `requires` on a descriptor is requires_invalid's (requires.go).
func checkMCP(k *kb.KB) []Finding {
	entries, err := os.ReadDir(filepath.Join(k.Root, "mcp"))
	if err != nil {
		return nil
	}
	var findings []Finding
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		rel := "mcp/" + e.Name()
		data, _, readErr := contract.ReadFileCapped(filepath.Join(k.Root, filepath.FromSlash(rel)))
		if readErr != nil {
			continue
		}
		if mcpHasRequires(data) {
			continue
		}
		if _, perr := contract.ParseMCPServerSpec(strings.TrimSuffix(e.Name(), ".json"), data); perr != nil {
			findings = append(findings, newFinding("mcp_invalid", Finding{
				Path:    rel,
				Message: fmt.Sprintf("mcp descriptor %s would be left out at sync: %v", e.Name(), perr),
			}))
		}
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Path < findings[j].Path })
	return findings
}
