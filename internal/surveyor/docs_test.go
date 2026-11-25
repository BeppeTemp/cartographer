package surveyor

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// caFileLine matches the top-level ca_file key of a documented example, up to its value.
var caFileLine = regexp.MustCompile(`(?m)^(ca_file:\s*)\S+`)

// The surveyor YAML in docs/deployment.md is loaded the way TestShippedAuthExamplesLoad
// loads the auth examples, so the documented config cannot rot. Production
// registers no task kind yet: a stand-in "ingest" lets the example load, and the
// plan that adds the real one replaces it.
func TestDocumentedSurveyorConfigLoads(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "deployment.md"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SURVEYOR_TOKEN", "example-token")
	t.Setenv("CLOUD_API_KEY", "example-key")
	t.Setenv("MODEL_API_KEY", "example-key")
	fixture, err := os.ReadFile(filepath.Join("testdata", "prompts", "weekly-digest.md"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, blk := range strings.Split(string(doc), "```yaml\n")[1:] {
		body, _, _ := strings.Cut(blk, "```")
		if !strings.Contains(body, "\nserver_url:") || !strings.Contains(body, "\ntasks:") {
			continue
		}
		n++
		dir := t.TempDir()
		// The prompt-task example names prompts/weekly-digest.md next to the config.
		if err := os.MkdirAll(filepath.Join(dir, "prompts"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "prompts", "weekly-digest.md"), fixture, 0o600); err != nil {
			t.Fatal(err)
		}
		// The documented ca_file is a POSIX path (/etc/...), which is not absolute on
		// Windows, where the validator rightly refuses it. The example is loaded with the
		// value replaced by an OS-absolute path in the temp dir; single quotes keep the
		// backslashes of a Windows path literal in YAML.
		body = caFileLine.ReplaceAllString(body, "${1}'"+filepath.Join(dir, "ca.pem")+"'")
		path := filepath.Join(dir, "surveyor.yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path, NewKinds(Kind{Name: "ingest"}, promptKind(), RepairKind())); err != nil {
			t.Errorf("docs/deployment.md surveyor example %d does not load: %v\n%s", n, err, body)
		}
	}
	if n == 0 {
		t.Fatal("no surveyor example found in docs/deployment.md")
	}
}

// The pages that describe the poll say what the shaping call sees of an entry's text.
func TestDocsSayWhatTheShapingCallSees(t *testing.T) {
	for _, page := range []string{"deployment.md", "surveyor-recipes.md"} {
		doc, err := os.ReadFile(filepath.Join("..", "..", "docs", page))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(doc), "`content` data block") || !strings.Contains(string(doc), "6 KiB") {
			t.Errorf("%s does not say that the shaping call gets a capped `content` data block", page)
		}
	}
	if pollContentMax != 6<<10 {
		t.Errorf("pollContentMax = %d: update the docs that say 6 KiB", pollContentMax)
	}
}
