package mcpserver

import (
	"fmt"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// findingOut is a lint finding as the tools return it (D289): the optional
// machine-readable fix is omitted when the finding has none.
type findingOut struct {
	Path     string    `json:"path"`
	Check    string    `json:"check"`
	Severity string    `json:"severity"`
	Message  string    `json:"message"`
	Fix      *lint.Fix `json:"fix,omitempty"`
}

func findingsOut(findings []lint.Finding) []findingOut {
	out := make([]findingOut, 0, len(findings))
	for _, f := range findings {
		out = append(out, findingOut{Path: f.Path, Check: f.Check, Severity: f.Severity, Message: f.Message, Fix: f.Fix})
	}
	return out
}

// rejectToolParamKeys refuses a frontmatter map that carries a write-tool
// parameter name as a key (D289): an agent that passed if_match, body... inside
// the frontmatter object, which would otherwise be persisted as a field. With
// allowNull a null value passes: in a patch it removes the key, which is the
// repair, not the mistake.
func rejectToolParamKeys(fm map[string]interface{}, allowNull bool) error {
	keys := make([]string, 0, len(fm))
	for k := range fm {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if lint.IsToolParamField(k) && !(allowNull && fm[k] == nil) {
			return fmt.Errorf("frontmatter key %q is a tool parameter, not a field — pass it as a top-level argument", k)
		}
	}
	return nil
}

// writeFindings returns the lint findings of a concept just written (D289),
// lint_ignore applied. The write has already succeeded: findings never fail it.
// Nil when there is nothing to say, so the response omits the key.
func writeFindings(k *kb.KB, id string) []findingOut {
	data, err := k.ReadConcept(okf.ConceptID(id))
	if err != nil {
		return nil
	}
	found := lint.CheckConcept(k, okf.ConceptID(id), data.Content)
	if len(found) == 0 {
		return nil
	}
	return findingsOut(found)
}
