package repodocs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// The second development client (Kiro CLI) reads its agent definitions from
// `.kiro/agents/<name>.json`. They are generated from `.claude/agents/<name>.md`
// and hold a pointer to the role's body, never a copy of it: the body stays in
// one place. `make kiro-agents` rewrites them.
//
// Trap: a relative `prompt` file path resolves against the agent file's folder,
// a `resources` path against the folder the session was started in. The
// definition is therefore a resource (the session starts in the main clone, so
// it is `main`'s) and the instruction to follow it is inline text.

// claudeModelPattern is the model id a definition pins: claude-<family>-<major>-<minor>.
var claudeModelPattern = regexp.MustCompile(`^(claude-[a-z]+-\d+)-(\d+)$`)

// kiroToolOf maps a Claude Code tool to the second client's. Write and Edit
// share one tool.
var kiroToolOf = map[string]string{
	"Read": "read", "Grep": "grep", "Glob": "glob", "Bash": "shell",
	"Write": "write", "Edit": "write",
}

// kiroDefaultTools is the set of a definition that lists no `tools`.
var kiroDefaultTools = []string{"read", "write", "shell", "grep", "glob"}

// kiroPrompt is the prompt of every role; %s is the role's name. A role that can
// write also gets kiroCommands, which sends it to the table of CONTRIBUTING.md for
// this client's commands. A read-only role does not: it would read that table from
// the worktree it reviews, which is the change under review.
const kiroPrompt = "Your definition is the repository file `.claude/agents/%s.md`, loaded as a resource and resolved against the folder this session was started in. " +
	"When it is not in your context, read it with the read tool from that folder; if it does not exist there, stop and report `definition not found: .claude/agents/%s.md`. " +
	"Follow its body to the letter and ignore its frontmatter: your model and tools come from this file. " +
	"Every command runs in the worktree the mandate names. " +
	"%sBefore editing under a directory, read the `AGENTS.md` of that directory and of each parent up to the root: this client does not load them on its own. " +
	"Your final report is the one the definition prescribes."

const kiroCommands = "Where `CONTRIBUTING.md` §Working with an agent client gives this client a different command, use that one. "

type kiroPermission struct {
	Tool   string `json:"tool"`
	Effect string `json:"effect"`
}

type kiroAgent struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Model          string   `json:"model"`
	Tools          []string `json:"tools"`
	IncludeMcpJSON bool     `json:"includeMcpJson"`
	Resources      []string `json:"resources"`
	Permissions    struct {
		Rules []kiroPermission `json:"rules"`
	} `json:"permissions"`
	Prompt string `json:"prompt"`
}

// GenerateKiroAgent returns the bytes of `.kiro/agents/<name>.json` for the
// content of `.claude/agents/<name>.md`.
func GenerateKiroAgent(name string, md []byte) ([]byte, error) {
	parts := strings.SplitN(string(md), "---\n", 3)
	if len(parts) != 3 || parts[0] != "" {
		return nil, fmt.Errorf("%s: no frontmatter", name)
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		Model       string `yaml:"model"`
		Tools       string `yaml:"tools"`
	}
	if err := yaml.Unmarshal([]byte(parts[1]), &fm); err != nil {
		return nil, fmt.Errorf("%s: the frontmatter is not valid YAML: %w", name, err)
	}
	if fm.Name != name {
		return nil, fmt.Errorf("%s: `name` is %q, want the file name", name, fm.Name)
	}
	m := claudeModelPattern.FindStringSubmatch(fm.Model)
	if m == nil {
		return nil, fmt.Errorf("%s: `model` %q is not a pinned claude-<family>-<major>-<minor> id", name, fm.Model)
	}
	tools := append([]string(nil), kiroDefaultTools...)
	if strings.TrimSpace(fm.Tools) != "" {
		tools = nil
		for _, t := range strings.Split(fm.Tools, ",") {
			t = strings.TrimSpace(t)
			k, ok := kiroToolOf[t]
			if !ok {
				return nil, fmt.Errorf("%s: the tool %q has no twin in the kiro tool table", name, t)
			}
			if !contains(tools, k) {
				tools = append(tools, k)
			}
		}
	}
	// The client's code-intelligence tool can rewrite code (pattern rewrite, rename), so
	// it follows the write tool: a role whose source cannot edit never gets it.
	if contains(tools, "write") {
		tools = append(tools, "code")
	}
	commands := ""
	if contains(tools, "write") {
		commands = kiroCommands
	}
	a := kiroAgent{
		Name: name, Description: fm.Description, Model: m[1] + "." + m[2],
		Tools: tools, Resources: []string{"file://.claude/agents/" + name + ".md"},
		Prompt: fmt.Sprintf(kiroPrompt, name, name, commands),
	}
	// No pre-approval the Claude source does not have: the shell is never auto-allowed.
	a.Permissions.Rules = []kiroPermission{{"fs_read", "allow"}}
	if contains(tools, "write") {
		a.Permissions.Rules = append(a.Permissions.Rules, kiroPermission{"fs_write", "allow"})
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(a); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// DevelopmentClients are the clients this repository is developed with, in the
// column order of the table in CONTRIBUTING.md §Working with an agent client,
// each with the test that checks its agent definitions.
var DevelopmentClients = []struct{ Name, DefinitionsTest string }{
	{"Claude Code", "TestClaudeSubagentsPinAModelAndReadOnlyOnesCannotEdit"},
	{"Kiro CLI", "TestKiroAgentsAreGeneratedFromTheClaudeOnes"},
}
