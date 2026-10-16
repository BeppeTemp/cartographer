package clientconfig

import (
	"os"
	"testing"
)

func TestReportMarksFileKeysAndDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(Path(dir), []byte("server_url: http://example.com/mcp\nupdate:\n  policy: auto-patch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Report(dir, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if r.ServerURL != "http://example.com/mcp" || r.Version != "1.2.3" || r.YAML == "" {
		t.Fatalf("report = %+v", r)
	}
	by := map[string]ReportEntry{}
	for _, e := range r.Entries {
		by[e.Key] = e
	}
	if len(by) != len(Schema.Keys) {
		t.Fatalf("%d entries, want one per schema key (%d)", len(by), len(Schema.Keys))
	}
	if e := by["update.policy"]; e.Origin != "file" || e.Value != "auto-patch" {
		t.Errorf("update.policy = %+v", e)
	}
	if e := by["search_depth"]; e.Origin != "default" || e.Value != 4 {
		t.Errorf("search_depth = %+v", e)
	}
}

func TestReportWithoutAFileIsAllDefaults(t *testing.T) {
	r, err := Report(t.TempDir(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range r.Entries {
		if e.Origin != "default" {
			t.Fatalf("%s: origin %q without a file", e.Key, e.Origin)
		}
	}
}
