package repodocs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The repository's subagents live only in `.claude/agents/` (D992): development
// targets Claude Code alone. What still matters there is that each one pins a
// Claude model, so a coordinator never hands a mandate to whatever the session
// default is, and that the reviewers, the readers and the auditors cannot edit.
var claudeModelID = regexp.MustCompile(`^claude-[a-z]+-\d+-\d+$`)

// readOnlyAgents are the agents that must not change the working copy.
var readOnlyAgents = map[string]bool{
	"cartographer-contract-reviewer": true,
	"cartographer-docs-auditor":      true,
	"cartographer-investigator":      true,
	"cartographer-plan-writer":       true,
	"cartographer-security-reviewer": true,
	"cartographer-source-reader":     true,
}

func TestClaudeSubagentsPinAModelAndReadOnlyOnesCannotEdit(t *testing.T) {
	root := repoRoot(t)
	mds, err := filepath.Glob(filepath.Join(root, ".claude", "agents", "*.md"))
	if err != nil || len(mds) == 0 {
		t.Fatalf("no .claude/agents/*.md: the check is not doing anything (%v)", err)
	}
	seen := map[string]bool{}
	for _, md := range mds {
		name := strings.TrimSuffix(filepath.Base(md), ".md")
		seen[name] = true
		raw, err := os.ReadFile(md)
		if err != nil {
			t.Error(err)
			continue
		}
		parts := strings.SplitN(string(raw), "---\n", 3)
		if len(parts) != 3 || parts[0] != "" {
			t.Errorf(".claude/agents/%s.md: no frontmatter", name)
			continue
		}
		var fm struct {
			Name  string `yaml:"name"`
			Model string `yaml:"model"`
			Tools string `yaml:"tools"`
		}
		if err := yaml.Unmarshal([]byte(parts[1]), &fm); err != nil {
			t.Errorf(".claude/agents/%s.md: the frontmatter is not valid YAML: %v", name, err)
			continue
		}
		if fm.Name != name {
			t.Errorf(".claude/agents/%s.md: `name` is %q, want the file name", name, fm.Name)
		}
		if !claudeModelID.MatchString(fm.Model) {
			t.Errorf(".claude/agents/%s.md: `model` is %q, want a pinned `claude-<family>-<major>-<minor>` id", name, fm.Model)
		}
		if readOnlyAgents[name] && fm.Tools != "Read, Grep, Glob, Bash" {
			t.Errorf(".claude/agents/%s.md: `tools` is %q, want `Read, Grep, Glob, Bash` (no Edit, no Write)", name, fm.Tools)
		}
	}
	for name := range readOnlyAgents {
		if !seen[name] {
			t.Errorf(".claude/agents/%s.md is missing", name)
		}
	}
}

// AC-P7-2: an implementer never runs the hosted check; the coordinator starts
// one on the tip beside the review (D-new-fast-review-loop).
func TestImplementerAgentsNeverRunCheckHosted(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"cartographer-plan-implementer", "cartographer-fix-implementer", "cartographer-live-implementer"} {
		raw, err := os.ReadFile(filepath.Join(root, ".claude", "agents", name+".md"))
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if !strings.Contains(strings.ToLower(text), "never `make check-hosted`") {
			t.Errorf(".claude/agents/%s.md: it must say the implementer never runs `make check-hosted`", name)
		}
		// Whatever is left once the "never" sentence and the description of what
		// the Windows leg runs are removed must not name the hosted check at all.
		rest := regexp.MustCompile("(?i)never `make check-hosted`").ReplaceAllString(text, "")
		rest = strings.ReplaceAll(rest, "the Windows leg of `make check-hosted` runs the test", "")
		if strings.Contains(rest, "check-hosted") {
			t.Errorf(".claude/agents/%s.md: `check-hosted` appears outside the \"never\" sentence: an implementer is not told to run it", name)
		}
	}
}
