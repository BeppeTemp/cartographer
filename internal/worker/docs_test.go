package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The worker YAML in docs/deployment.md is loaded the way TestShippedAuthExamplesLoad
// loads the auth examples, so the documented config cannot rot. Production
// registers no task kind yet: a stand-in "ingest" lets the example load, and the
// plan that adds the real one replaces it.
func TestDocumentedWorkerConfigLoads(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "deployment.md"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKER_TOKEN", "example-token")
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
		path := filepath.Join(dir, "worker.yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path, NewKinds(Kind{Name: "ingest"}, promptKind())); err != nil {
			t.Errorf("docs/deployment.md worker example %d does not load: %v\n%s", n, err, body)
		}
	}
	if n == 0 {
		t.Fatal("no worker example found in docs/deployment.md")
	}
}
