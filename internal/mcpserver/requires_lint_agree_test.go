package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

// The write gate and lint answer one question with one call
// (provisioning.ValidateRequires): every example, accepted or refused by
// validateArtifactContent, must be accepted or reported as requires_invalid
// by an unscoped lint of the same file arriving by git.
func TestRequiresGateAndLintAgree(t *testing.T) {
	skillFM := func(r string) string {
		return "---\nname: me\ndescription: does a thing\n" + r + "---\nBody.\n"
	}
	agentFM := func(r string) string {
		return "---\nname: me\ndescription: does a thing\n" + r + "---\nBody.\n"
	}
	hook := func(r string) string {
		return `{"event":"Stop","command":"./run.sh"` + r + `}`
	}
	cases := []struct {
		name, kind, rel, content string
		bad                      bool
	}{
		{"skill ok flow", "skill", "skills/me/SKILL.md", skillFM("requires: [skill:other, agent:helper, hook:h]\n"), false},
		{"skill ok block", "skill", "skills/me/SKILL.md", skillFM("requires:\n  - agent:helper\n"), false},
		{"skill ok capability", "skill", "skills/me/SKILL.md", skillFM("requires: [agent.delegate]\n"), false},
		{"skill none", "skill", "skills/me/SKILL.md", skillFM(""), false},
		{"skill scalar", "skill", "skills/me/SKILL.md", skillFM("requires: agent:helper\n"), true},
		{"skill self", "skill", "skills/me/SKILL.md", skillFM("requires: [skill:me]\n"), true},
		{"skill unknown kind", "skill", "skills/me/SKILL.md", skillFM("requires: [tool:x]\n"), true},
		{"skill mcp", "skill", "skills/me/SKILL.md", skillFM("requires: [mcp:x]\n"), true},
		{"skill negated", "skill", "skills/me/SKILL.md", skillFM("requires: [\"!agent:x\"]\n"), true},
		{"skill duplicate", "skill", "skills/me/SKILL.md", skillFM("requires: [agent:x, agent:x]\n"), true},
		{"skill unknown capability", "skill", "skills/me/SKILL.md", skillFM("requires: [nope.cap]\n"), true},
		{"agent ok", "agent", "agents/me.md", agentFM("requires: [skill:other]\n"), false},
		{"agent self", "agent", "agents/me.md", agentFM("requires: [agent:me]\n"), true},
		{"agent bad name", "agent", "agents/me.md", agentFM("requires: [skill:../x]\n"), true},
		{"hook ok", "hook", "hooks/me/hook.json", hook(`,"requires":["skill:a","agent.delegate"]`), false},
		{"hook none", "hook", "hooks/me/hook.json", hook(""), false},
		{"hook string", "hook", "hooks/me/hook.json", hook(`,"requires":"skill:a"`), true},
		{"hook null", "hook", "hooks/me/hook.json", hook(`,"requires":null`), true},
		{"hook self", "hook", "hooks/me/hook.json", hook(`,"requires":["hook:me"]`), true},
		{"instructions ok", "instructions", "instructions.md", "---\ntitle: x\n---\nBody.\n", false},
		{"instructions requires", "instructions", "instructions.md", "---\nrequires: [skill:a]\n---\nBody.\n", true},
		{"mcp requires", "mcp", "mcp/me.json", `{"type":"http","url":"https://example.com/mcp","requires":["skill:a"]}`, true},
		{"mcp ok", "mcp", "mcp/me.json", `{"type":"http","url":"https://example.com/mcp"}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name := ""
			if c.kind != "instructions" {
				name = "me"
			}
			gateErr := validateArtifactContent(artifactPathInfo{Kind: c.kind, Name: name}, c.rel, []byte(c.content))

			k := kbtest.New(t)
			abs := filepath.Join(k.Root, filepath.FromSlash(c.rel))
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(abs, []byte(c.content), 0o644); err != nil {
				t.Fatal(err)
			}
			findings, err := lint.Run(k, "", false)
			if err != nil {
				t.Fatal(err)
			}
			lintHit := false
			for _, f := range findings {
				if f.Check == "requires_invalid" && f.Path == c.rel {
					lintHit = true
				}
			}
			if (gateErr != nil) != c.bad {
				t.Errorf("gate: bad=%v, err=%v", c.bad, gateErr)
			}
			if lintHit != c.bad {
				t.Errorf("lint: bad=%v, requires_invalid=%v (gate err: %v)", c.bad, lintHit, gateErr)
			}
			if lintHit && gateErr != nil && c.kind != "mcp" && !strings.Contains(strings.Join(messages(findings, c.rel), " "), "requires") {
				t.Errorf("lint message does not mention requires")
			}
		})
	}
}

func messages(fs []lint.Finding, path string) []string {
	var out []string
	for _, f := range fs {
		if f.Path == path {
			out = append(out, f.Message)
		}
	}
	return out
}
