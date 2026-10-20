package clientconfig

import (
	"os"
	"strings"
	"testing"
)

func TestReportMarksFileKeysAndDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(Path(dir), []byte("server_url: http://example.com/mcp\nupdate_check: false\n"), 0o600); err != nil {
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
	if e := by["update_check"]; e.Origin != "file" || e.Value != false {
		t.Errorf("update_check = %+v", e)
	}
	if e := by["search_roots"]; e.Origin != "default" || len(e.Value.([]any)) != 4 {
		t.Errorf("search_roots = %+v", e)
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

// The token of a client never leaves the machine: a literal one is masked in
// the entries and in the file the report carries (D818), as the server's own
// secrets are in its settings view.
func TestReportMasksTheTokenInEntriesAndYAML(t *testing.T) {
	dir := t.TempDir()
	file := "# the bearer token\ntoken: s3cret # s3cret\nserver_url: http://example.com/mcp\n# token: s3cret-old\n"
	if err := os.WriteFile(Path(dir), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Report(dir, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.YAML, "s3cret") {
		t.Errorf("the token is in the report's yaml: %q", r.YAML)
	}
	for _, want := range []string{"# the bearer token", "token: " + SecretRedacted, "server_url: http://example.com/mcp", "# token: " + SecretRedacted} {
		if !strings.Contains(r.YAML, want) {
			t.Errorf("yaml lost %q: %q", want, r.YAML)
		}
	}
	for _, e := range r.Entries {
		switch {
		case e.Key == "token" && (e.Value != SecretSet || e.Origin != "file"):
			t.Errorf("token entry = %+v, want %q from the file", e, SecretSet)
		case e.Key == "server_url" && e.Value != "http://example.com/mcp":
			t.Errorf("a key that is no secret was changed: %+v", e)
		}
	}
}

func TestReportKeepsAFileWithoutASecretAsItIs(t *testing.T) {
	// A file Save wrote lists the default token commented out, and one that
	// sets none has nothing to mask: it goes as it is, byte for byte.
	listed, err := Schema.Format([]byte("update_check: false\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"", "update_check: false\n", "token: \"\"\n", "token:\n", string(listed)} {
		r, err := report([]byte(file), "m", "dev")
		if err != nil {
			t.Fatal(err)
		}
		if r.YAML != file {
			t.Errorf("yaml of %q = %q", file, r.YAML)
		}
		for _, e := range r.Entries {
			if e.Key == "token" && e.Value != SecretUnset {
				t.Errorf("token entry of %q = %+v, want %q", file, e, SecretUnset)
			}
		}
	}
}

func TestRedactYAMLMasksEveryWayOfWritingTheToken(t *testing.T) {
	for name, file := range map[string]string{
		"plain":      "token: s3cret\n",
		"quoted":     "token: 's3cret'\n",
		"flow":       "{token: s3cret}\n",
		"block":      "token: >\n  s3cret\n",
		"anchored":   "token: &t s3cret\nserver_name: *t\n",
		"alias":      "other: &t s3cret\ntoken: *t\n",
		"duplicated": "token: first\ntoken: s3cret\n",
		"number":     "token: 123456\n",
	} {
		out, err := RedactYAML([]byte(file))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(string(out), "s3cret") || strings.Contains(string(out), "123456") || strings.Contains(string(out), "first") ||
			!strings.Contains(string(out), SecretRedacted) {
			t.Errorf("%s: %q", name, out)
		}
	}
	if _, err := RedactYAML([]byte("token: s3cret\n  - : [\n")); err == nil {
		t.Error("a file that is not YAML cannot be checked, and says so")
	}
}
