package repodocs

import (
	"encoding/json"
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

// AC-P6-4, AC-P6-5, AC-P6-6: the second client's definitions are generated from
// the Claude ones, one file per role, and nothing else matches the prefix.
func TestKiroAgentsAreGeneratedFromTheClaudeOnes(t *testing.T) {
	root := repoRoot(t)
	mds, _ := filepath.Glob(filepath.Join(root, ".claude", "agents", "cartographer-*.md"))
	if len(mds) == 0 {
		t.Fatal("no .claude/agents/cartographer-*.md: the check is not doing anything")
	}
	fix := "make kiro-agents"
	want := map[string]bool{}
	for _, md := range mds {
		name := strings.TrimSuffix(filepath.Base(md), ".md")
		raw, err := os.ReadFile(md)
		if err != nil {
			t.Fatal(err)
		}
		gen, err := GenerateKiroAgent(name, raw)
		if err != nil {
			t.Error(err)
			continue
		}
		rel := filepath.Join(".kiro", "agents", name+".json")
		want[rel] = true
		for _, line := range strings.Split(strings.SplitN(string(raw), "---\n", 3)[2], "\n") {
			if len(line) >= 30 && strings.Contains(string(gen), line) {
				t.Errorf("%s holds a line of the role's body: a definition carries a pointer, never a copy", rel)
				break
			}
		}
		if *update {
			// Never write through a symlink: it could point outside the tree.
			for _, p := range []string{filepath.Join(root, ".kiro"), filepath.Join(root, ".kiro", "agents"), filepath.Join(root, rel)} {
				if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
					t.Fatalf("%s is a symlink: make kiro-agents refuses to write through it", p)
				}
			}
			if err := os.MkdirAll(filepath.Join(root, ".kiro", "agents"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, rel), gen, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil || string(got) != string(gen) {
			t.Errorf("%s is missing or differs from its generator. Run `%s` and commit the result.", rel, fix)
		}
		var def kiroAgent
		if err := json.Unmarshal(gen, &def); err != nil {
			t.Fatal(err)
		}
		if readOnlyAgents[name] && (contains(def.Tools, "write") || contains(def.Tools, "code")) {
			t.Errorf("%s: a read-only role has the write or the code tool (code can rewrite)", rel)
		}
		for _, r := range def.Permissions.Rules {
			if r.Effect != "allow" || (r.Tool != "fs_read" && r.Tool != "fs_write") || (r.Tool == "fs_write" && !contains(def.Tools, "write")) {
				t.Errorf("%s: the rule %+v is a pre-approval the .claude source does not have", rel, r)
			}
		}
	}
	existing, _ := filepath.Glob(filepath.Join(root, ".kiro", "agents", "cartographer-*.json"))
	for _, f := range existing {
		rel, _ := filepath.Rel(root, f)
		if !want[rel] {
			if *update {
				_ = os.Remove(f)
				continue
			}
			t.Errorf("%s has no .claude/agents source. Run `%s` or delete it.", rel, fix)
		}
	}
}
