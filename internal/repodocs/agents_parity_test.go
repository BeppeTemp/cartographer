package repodocs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The repository's own coding subagents (plan-implementer, live-implementer)
// exist once per client format and must say the same thing (D327): a coordinator
// on one client and a coordinator on another hand the same mandate to a worker
// with the same rules. Edits land in one file and not the others unless this
// fails, because nothing else reads all three.
func TestRepoSubagentsSayTheSameThingOnEveryClient(t *testing.T) {
	root := repoRoot(t)
	mds, err := filepath.Glob(filepath.Join(root, ".claude", "agents", "*.md"))
	if err != nil || len(mds) == 0 {
		t.Fatalf("no .claude/agents/*.md: the parity check is not doing anything (%v)", err)
	}
	for _, md := range mds {
		name := strings.TrimSuffix(filepath.Base(md), ".md")
		want := claudeAgentBody(t, md)

		codex := readText(t, filepath.Join(root, ".codex", "agents", name+".toml"))
		const open, closing = "developer_instructions = \"\"\"\n", "\n\"\"\""
		i := strings.Index(codex, open)
		j := strings.LastIndex(codex, closing)
		if i < 0 || j < i {
			t.Errorf(".codex/agents/%s.toml: no developer_instructions block", name)
		} else if got := strings.TrimSpace(codex[i+len(open) : j]); got != want {
			t.Errorf(".codex/agents/%s.toml says something else than .claude/agents/%s.md", name, name)
		}

		var kiro struct {
			Name   string `json:"name"`
			Prompt string `json:"prompt"`
		}
		if err := json.Unmarshal([]byte(readText(t, filepath.Join(root, ".kiro", "agents", name+".json"))), &kiro); err != nil {
			t.Errorf(".kiro/agents/%s.json: %v", name, err)
		} else if kiro.Name != name || strings.TrimSpace(kiro.Prompt) != want {
			t.Errorf(".kiro/agents/%s.json says something else than .claude/agents/%s.md", name, name)
		}
	}
}

func claudeAgentBody(t *testing.T, path string) string {
	t.Helper()
	parts := strings.SplitN(readText(t, path), "---\n", 3)
	if len(parts) != 3 {
		t.Fatalf("%s: no frontmatter", path)
	}
	return strings.TrimSpace(parts[2])
}

func readText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}
