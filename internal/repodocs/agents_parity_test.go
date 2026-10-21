package repodocs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The repository's own subagents (cartographer-plan-implementer, cartographer-live-implementer,
// cartographer-contract-reviewer, cartographer-security-reviewer, cartographer-source-reader) exist once per client format and must
// say the same thing (D327, D884): a coordinator on one client and a coordinator
// on another hand the same mandate to a worker with the same rules, on the same
// model. `.claude/agents/<name>.md` is the reference, and for each of its agents
// the Kiro (`.kiro/agents/<name>.json`), OpenCode (`.opencode/agents/<name>.md`)
// and Copilot CLI (`.github/agents/<name>.agent.md`) files must carry the same
// prompt body, the same description and the Claude model spelled the way that
// client spells it. A file in a client directory with no Claude counterpart is
// refused too, so a client cannot keep an agent the others lost. Edits land in
// one file and not the others unless this fails, because nothing else reads all
// four.
func TestRepoSubagentsSayTheSameThingOnEveryClient(t *testing.T) {
	root := repoRoot(t)
	mds, err := filepath.Glob(filepath.Join(root, ".claude", "agents", "*.md"))
	if err != nil || len(mds) == 0 {
		t.Fatalf("no .claude/agents/*.md: the parity check is not doing anything (%v)", err)
	}

	known := map[string]bool{}
	for _, md := range mds {
		name := strings.TrimSuffix(filepath.Base(md), ".md")
		known[name] = true
		ref, ok := loadMarkdownAgent(t, md)
		if !ok {
			continue
		}
		if ref.Name != name {
			t.Errorf(".claude/agents/%s.md: `name` is %q, want the file name", name, ref.Name)
		}
		dotted, ok := dottedModel(ref.Model)
		if !ok {
			t.Errorf(".claude/agents/%s.md: model %q is not `claude-<family>-<major>-<minor>`, "+
				"so the other clients' spelling cannot be derived from it (D884)", name, ref.Model)
			continue
		}

		// Kiro: JSON, the prompt is the body.
		var kiro struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Model       string   `json:"model"`
			Prompt      string   `json:"prompt"`
			Tools       []string `json:"tools"`
		}
		kiroPath := filepath.Join(".kiro", "agents", name+".json")
		if raw, err := os.ReadFile(filepath.Join(root, kiroPath)); err != nil {
			t.Errorf("%s: %v", kiroPath, err)
		} else if err := json.Unmarshal(raw, &kiro); err != nil {
			t.Errorf("%s: %v", kiroPath, err)
		} else {
			if kiro.Name != name {
				t.Errorf("%s: `name` is %q, want the file name", kiroPath, kiro.Name)
			}
			agentMatches(t, kiroPath, name, agentSpec{Description: kiro.Description, Model: kiro.Model, Body: strings.TrimSpace(kiro.Prompt)}, ref, dotted)
			// A Kiro agent with no `tools` has no tools at all (probe, D300): an
			// agent that loads but cannot read a file looks like it works.
			if len(kiro.Tools) == 0 {
				t.Errorf("%s: no `tools`: a Kiro agent without them is offered no tool, so list them (`[\"*\"]` when the client has no narrower read set) (#957)", kiroPath)
			}
		}

		// OpenCode: Markdown, the name is the file name, a model carries its provider.
		ocPath := filepath.Join(".opencode", "agents", name+".md")
		if oc, ok := loadMarkdownAgent(t, filepath.Join(root, ocPath)); ok {
			agentMatches(t, ocPath, name, oc, ref, "anthropic/"+ref.Model)
			if oc.Mode != "subagent" {
				t.Errorf("%s: `mode` is %q, want subagent: the coordinator spawns it, a user does not select it", ocPath, oc.Mode)
			}
		}

		// Copilot CLI: Markdown with its own `.agent.md` suffix and a `name`.
		cpPath := filepath.Join(".github", "agents", name+".agent.md")
		if cp, ok := loadMarkdownAgent(t, filepath.Join(root, cpPath)); ok {
			agentMatches(t, cpPath, name, cp, ref, dotted)
			if cp.Name != name {
				t.Errorf("%s: `name` is %q, want the file name", cpPath, cp.Name)
			}
		}
	}

	// A read-only agent is read-only in every client that can say so (#957):
	// Claude Code and Copilot CLI list the tools it may use, OpenCode takes a
	// permission block. Kiro has no read-only set in the ledger, so its copy
	// keeps the restriction in the prompt. A copy that lost its restriction would
	// let the agent edit.
	for _, name := range readOnlyAgents {
		cl, ok := loadMarkdownAgent(t, filepath.Join(root, ".claude", "agents", name+".md"))
		if ok {
			if got, _ := cl.Tools.(string); got != "Read, Grep, Glob, Bash" {
				t.Errorf(".claude/agents/%s.md: `tools` is %v, want `Read, Grep, Glob, Bash`", name, cl.Tools)
			}
		}
		cp, ok := loadMarkdownAgent(t, filepath.Join(root, ".github", "agents", name+".agent.md"))
		if ok {
			tools, _ := cp.Tools.([]any)
			if len(tools) == 0 || slices.Contains(tools, any("edit")) || slices.Contains(tools, any("*")) {
				t.Errorf(".github/agents/%s.agent.md: `tools` is %v, want a read/search/execute list without edit", name, cp.Tools)
			}
		}
		// Kiro lists ["*"] (no read-only tool names in the ledger, D300), so the
		// restriction is the prompt's own sentence: pin it.
		if raw, err := os.ReadFile(filepath.Join(root, ".kiro", "agents", name+".json")); err == nil {
			var k struct {
				Prompt string `json:"prompt"`
			}
			if json.Unmarshal(raw, &k) == nil && !strings.Contains(k.Prompt, "read-only, then stop") {
				t.Errorf(".kiro/agents/%s.json: the prompt lost its \"read-only, then stop\" sentence, the only read-only restriction Kiro has", name)
			}
		}
		oc, ok := loadMarkdownAgent(t, filepath.Join(root, ".opencode", "agents", name+".md"))
		if ok && oc.Permission["edit"] != "deny" {
			t.Errorf(".opencode/agents/%s.md: `permission.edit` is %v, want deny", name, oc.Permission["edit"])
		}
	}

	// No orphans: every agent file of a client has a Claude counterpart.
	for _, c := range []struct{ dir, suffix string }{
		{".kiro/agents", ".json"},
		{".opencode/agents", ".md"},
		{".github/agents", ".agent.md"},
	} {
		files, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(c.dir), "*"))
		if err != nil {
			t.Fatalf("%s: %v", c.dir, err)
		}
		for _, f := range files {
			base := filepath.Base(f)
			if name := strings.TrimSuffix(base, c.suffix); !strings.HasSuffix(base, c.suffix) || !known[name] {
				t.Errorf("%s/%s has no .claude/agents counterpart: add the Claude file, or delete this one (D884)", c.dir, base)
			}
		}
	}
}

// readOnlyAgents are the agents that must not change the working copy.
var readOnlyAgents = []string{"cartographer-contract-reviewer", "cartographer-security-reviewer", "cartographer-source-reader"}

// agentSpec is what one client's agent file says, in the fields the parity
// check compares; Mode and Permission are OpenCode's, Name the Markdown ones.
type agentSpec struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Model       string         `yaml:"model"`
	Mode        string         `yaml:"mode"`
	Permission  map[string]any `yaml:"permission"`
	Tools       any            `yaml:"tools"`
	Body        string         `yaml:"-"`
}

// agentMatches reports every way a client's copy differs from the Claude one.
func agentMatches(t *testing.T, path, name string, got, ref agentSpec, wantModel string) {
	t.Helper()
	if got.Body != ref.Body {
		t.Errorf("%s says something else than .claude/agents/%s.md (the prompt body)", path, name)
	}
	if got.Description != ref.Description {
		t.Errorf("%s: `description` differs from .claude/agents/%s.md:\n  got  %q\n  want %q", path, name, got.Description, ref.Description)
	}
	if got.Model != wantModel {
		t.Errorf("%s: `model` is %q, want %q (the Claude model %q in this client's spelling, D884)", path, got.Model, wantModel, ref.Model)
	}
}

// dottedModel turns a Claude Code model id, `claude-sonnet-5-5`, into the id Kiro
// and Copilot CLI use for the same model, `claude-sonnet-5.5`. ok is false for an
// id that is not in the first shape, which is how a model that is not a Claude
// one fails the check instead of passing through it.
func dottedModel(id string) (string, bool) {
	m := regexp.MustCompile(`^(claude-[a-z]+-\d+)-(\d+)$`).FindStringSubmatch(id)
	if m == nil {
		return "", false
	}
	return m[1] + "." + m[2], true
}

// loadMarkdownAgent reads a Markdown agent file: YAML frontmatter, then the
// prompt body. A file it cannot read or parse is reported and skipped, so one
// missing client file does not hide the rest.
func loadMarkdownAgent(t *testing.T, path string) (agentSpec, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("%v", err)
		return agentSpec{}, false
	}
	parts := strings.SplitN(string(raw), "---\n", 3)
	if len(parts) != 3 || parts[0] != "" {
		t.Errorf("%s: no frontmatter", path)
		return agentSpec{}, false
	}
	var spec agentSpec
	if err := yaml.Unmarshal([]byte(parts[1]), &spec); err != nil {
		t.Errorf("%s: the frontmatter is not valid YAML: %v", path, err)
		return agentSpec{}, false
	}
	spec.Body = strings.TrimSpace(parts[2])
	return spec, true
}
