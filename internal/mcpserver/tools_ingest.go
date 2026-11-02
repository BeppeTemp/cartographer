package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// toolIngestSetup writes the two ingest templates when absent (D1201). A plan
// and its units are written with the ordinary concept tools, so unlike a Source
// (D1072) no call of theirs can carry the template as a side effect: this one
// idempotent tool does what ensureSourceTemplate does for the file part, and
// nothing else. It declares no template on any map (that makes a map strict,
// and the planner chooses the Maps), takes no lock and makes no commit: gitWrap
// does both.
func toolIngestSetup(k *kb.KB) Tool {
	return Tool{
		Name:        "ingest_setup",
		Description: "Writes templates/ingest-plan.md and templates/ingest-unit.md when absent (idempotent, nothing overwritten). Declares no map: map_create / map_update next.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			files := []struct{ slug, typ, text string }{
				{"ingest-plan", kb.IngestPlanType, kb.IngestPlanTemplate()},
				{"ingest-unit", kb.IngestUnitType, kb.IngestUnitTemplate()},
			}
			written, present, notes, warnings := []string{}, []string{}, []string{}, []string{}
			for _, f := range files {
				rel := "templates/" + f.slug + ".md"
				abs, err := k.ResolveRootPath(rel)
				if err == nil {
					err = rejectArtifactSymlinks(k.Root, rel)
				}
				if err != nil {
					warnings = append(warnings, fmt.Sprintf("%s not written: %v", rel, err))
					continue
				}
				data, rerr := os.ReadFile(abs)
				switch {
				case rerr == nil:
					present = append(present, rel)
					if info, ok := kb.ParseTemplate(f.slug, string(data)); !ok || !strings.EqualFold(info.Type, f.typ) {
						notes = append(notes, rel+" exists and is not an "+f.typ+" template: left as it is")
					}
				case errors.Is(rerr, os.ErrNotExist):
					if err := os.MkdirAll(filepath.Dir(abs), 0o755); err == nil {
						err = os.WriteFile(abs, []byte(f.text), 0o644)
						if err != nil {
							warnings = append(warnings, fmt.Sprintf("%s not written: %v", rel, err))
							continue
						}
						written = append(written, rel)
					} else {
						warnings = append(warnings, fmt.Sprintf("%s not written: %v", rel, err))
					}
				default:
					warnings = append(warnings, fmt.Sprintf("%s not read: %v", rel, rerr))
				}
			}
			notes = append(notes, "no map was created or declared: choose the Maps with map_create, and declare the templates on one with map_update only if you want it strict")
			resp := map[string]interface{}{"written": written, "present": present, "notes": notes}
			if len(warnings) > 0 {
				resp["warnings"] = warnings
			}
			return jsonResult(resp), nil
		},
	}
}
