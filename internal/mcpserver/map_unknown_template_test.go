package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func warningsOf(t *testing.T, out string) []string {
	t.Helper()
	var r struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	return r.Warnings
}

// #2278: a template slug the catalog does not know is a warning, never a
// refusal: the contract is written and the answer says what will happen.
func TestMapUpdateAndCreateWarnOnAnUnknownTemplate(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 0)
	write := func(name, content string) {
		t.Helper()
		abs := filepath.Join(k.Root, "templates", name)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("runbook.md", "---\ntype: Runbook\ntitle: \"{{title}}\"\n---\n# {{title}}\n")
	write("broken.md", "no frontmatter at all\n")

	// unknown slug: warns, names the known ones, and the contract is written
	out := callOK(t, s, "map_update", `{"map":"ops","templates":["runbook","nosuchtpl"]}`)
	w := warningsOf(t, out)
	if len(w) != 1 || !strings.Contains(w[0], `"nosuchtpl"`) || !strings.Contains(w[0], "known: runbook") || !strings.Contains(w[0], "templates/nosuchtpl.md") {
		t.Fatalf("warnings = %q", w)
	}
	if !strings.Contains(out, `"status":"updated"`) {
		t.Fatalf("not updated: %s", out)
	}
	if c, err := k.ReadMapContract("ops"); err != nil || len(c.Templates) != 2 {
		t.Fatalf("contract not written: %v %v", c.Templates, err)
	}

	// known slug: no warnings key at all
	out = callOK(t, s, "map_update", `{"map":"ops","templates":["runbook"]}`)
	if strings.Contains(out, `"warnings"`) {
		t.Fatalf("known template warned: %s", out)
	}
	// an update that names no template key stays silent
	if out = callOK(t, s, "map_update", `{"map":"ops","title":"Ops"}`); strings.Contains(out, "names no template") {
		t.Fatalf("no template key, warned: %s", out)
	}

	// a file that does not parse is not in the catalog
	if w := warningsOf(t, callOK(t, s, "map_update", `{"map":"ops","templates":["broken"],"reason":"test"}`)); len(w) != 1 || !strings.Contains(w[0], `"broken"`) {
		t.Fatalf("unparseable template: %q", w)
	}

	// default_template: the one of the list that is unknown, named once
	w = warningsOf(t, callOK(t, s, "map_update", `{"map":"ops","templates":["runbook","ghost"],"default_template":"ghost","reason":"test"}`))
	if len(w) != 1 || !strings.Contains(w[0], `"ghost"`) {
		t.Fatalf("default_template: %q", w)
	}

	// map_create: an unparseable file is no template either
	w = warningsOf(t, callOK(t, s, "map_create", `{"name":"fresh3","title":"Fresh","templates":["broken"]}`))
	if len(w) != 1 || !strings.Contains(w[0], `"broken"`) || !strings.Contains(w[0], "known: runbook") {
		t.Fatalf("map_create unparseable: %q", w)
	}

	// map_create: same helper
	w = warningsOf(t, callOK(t, s, "map_create", `{"name":"fresh","title":"Fresh","templates":["runbook","nosuchtpl"]}`))
	if len(w) != 1 || !strings.Contains(w[0], `"nosuchtpl"`) {
		t.Fatalf("map_create: %q", w)
	}
	if out = callOK(t, s, "map_create", `{"name":"fresh2","title":"Fresh","templates":["runbook"]}`); strings.Contains(out, `"warnings"`) {
		t.Fatalf("map_create known template warned: %s", out)
	}
}
