package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientvariant"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// checkVariants reports every variants/<name>.yaml that artifact_write would
// now refuse, so a descriptor that arrived by git surfaces here instead of being
// left out of the manifest silently at every client's sync. Warning-level like
// hook_invalid: the file is already in the KB and the fix is the author's.
func checkVariants(k *kb.KB) []Finding {
	entries, err := os.ReadDir(filepath.Join(k.Root, "variants"))
	if err != nil {
		return nil // no variants/ directory, nothing to check
	}
	var findings []Finding
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		rel := "variants/" + e.Name()
		data, readErr := os.ReadFile(filepath.Join(k.Root, filepath.FromSlash(rel)))
		var problem error
		if readErr != nil {
			problem = readErr
		} else {
			_, problem = clientvariant.Parse(strings.TrimSuffix(e.Name(), ".yaml"), data)
		}
		if problem == nil {
			continue
		}
		findings = append(findings, newFinding("variant_invalid", Finding{
			Path:    rel,
			Message: fmt.Sprintf("client variant %q would be left out at sync: %v", strings.TrimSuffix(e.Name(), ".yaml"), problem),
		}))
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Path < findings[j].Path })
	return findings
}
