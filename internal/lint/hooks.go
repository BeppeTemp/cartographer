package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// checkHooks reports every hooks/<name>/hook.json that artifact_write would now
// refuse (D284), so a KB written before that validation surfaces its broken
// hooks instead of skipping them silently at every client's sync. Warning, not
// error: the hook is already in the KB and the fix is the author's.
//
// A hook directory with no hook.json is reported too: it materializes and is
// counted, and nothing can fire it.
func checkHooks(k *kb.KB) []Finding {
	entries, err := os.ReadDir(filepath.Join(k.Root, "hooks"))
	if err != nil {
		return nil // no hooks/ directory, nothing to check
	}
	var findings []Finding
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rel := "hooks/" + e.Name() + "/hook.json"
		data, readErr := os.ReadFile(filepath.Join(k.Root, filepath.FromSlash(rel)))
		var problem error
		if readErr != nil {
			problem = fmt.Errorf("hook.json not found")
		} else {
			_, problem = provisioning.ValidateHookJSON(data)
		}
		msg := ""
		switch {
		case problem != nil:
			msg = fmt.Sprintf("hook %q would be skipped at sync, never firing: %v", e.Name(), problem)
		case provisioning.UnknownHookEvent(data) != "":
			// Registered as declared, so not skipped; a typo still never fires.
			msg = fmt.Sprintf("hook %q: %s", e.Name(), provisioning.UnknownHookEvent(data))
		default:
			continue
		}
		findings = append(findings, newFinding("hook_invalid", Finding{
			Path:    rel,
			Message: msg,
		}))
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Path < findings[j].Path })
	return findings
}
