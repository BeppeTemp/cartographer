package workerconfig

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/settingschema"
)

func TestSchemaEntriesAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range Schema.Keys {
		if seen[k.Path] {
			t.Errorf("%s is declared twice", k.Path)
		}
		seen[k.Path] = true
		if strings.TrimSpace(k.Description) == "" || strings.TrimSpace(k.Default) == "" || k.Type == "" {
			t.Errorf("%s: description, default and type are required", k.Path)
		}
		if k.Scope != settingschema.ScopeWorker {
			t.Errorf("%s: scope %q, want worker", k.Path, k.Scope)
		}
		if k.Description != "" && (k.Description[0] < 'A' || k.Description[0] > 'Z') {
			t.Errorf("%s: a description starts with a capital, or it reads as a commented-out key", k.Path)
		}
		if !strings.HasSuffix(k.Description, ".") {
			t.Errorf("%s: a description is one sentence ending in a full stop", k.Path)
		}
	}
}

// The conventions that keep the table usable for a redacted report (D22): the
// file holds names of variables, never secrets.
func TestSchemaConventions(t *testing.T) {
	for _, k := range Schema.Keys {
		last := k.Path[strings.LastIndexAny(k.Path, ".]")+1:]
		if k.Secret {
			t.Errorf("%s is Secret: the worker file holds no secret, only the names of variables", k.Path)
		}
		if strings.HasSuffix(last, "_env") != (k.Type == "env") {
			t.Errorf("%s: a *_env key has Type env, and only such a key does (Type %q)", k.Path, k.Type)
		}
		if (strings.HasSuffix(last, "_url") || last == "base_url" || last == "url") != (k.Type == "url") {
			t.Errorf("%s: a URL key has Type url, and only such a key does (Type %q)", k.Path, k.Type)
		}
	}
}
