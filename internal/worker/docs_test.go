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
	n := 0
	for _, blk := range strings.Split(string(doc), "```yaml\n")[1:] {
		body, _, _ := strings.Cut(blk, "```")
		if !strings.Contains(body, "\nserver_url:") || !strings.Contains(body, "\ntasks:") {
			continue
		}
		n++
		path := filepath.Join(t.TempDir(), "worker.yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path, NewKinds(Kind{Name: "ingest"})); err != nil {
			t.Errorf("docs/deployment.md worker example %d does not load: %v\n%s", n, err, body)
		}
	}
	if n == 0 {
		t.Fatal("no worker example found in docs/deployment.md")
	}
}
