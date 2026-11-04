package mcpserver

import (
	"strings"
	"testing"
)

// The write gate refuses a malformed `requires` through each carrier and
// accepts the documented forms (D1656).
func TestValidateArtifactContent_Requires(t *testing.T) {
	skill := artifactPathInfo{Kind: "skill", Name: "a"}
	agent := artifactPathInfo{Kind: "agent", Name: "a"}
	hook := artifactPathInfo{Kind: "hook", Name: "a"}
	instr := artifactPathInfo{Kind: "instructions"}
	cases := []struct {
		name    string
		info    artifactPathInfo
		rel     string
		content string
		wantErr string
	}{
		{"skill ok", skill, "skills/a/SKILL.md", "---\nname: a\ndescription: d\nrequires: [agent:b, skill]\n---\nbody\n", ""},
		{"skill block list", skill, "skills/a/SKILL.md", "---\nname: a\ndescription: d\nrequires:\n  - agent:b\n  - agent.invoke\n---\nbody\n", ""},
		{"skill scalar", skill, "skills/a/SKILL.md", "---\nname: a\ndescription: d\nrequires: agent:b\n---\n", "list of strings"},
		{"skill mcp", skill, "skills/a/SKILL.md", "---\nname: a\ndescription: d\nrequires: [mcp:x]\n---\n", "mcp cannot be required"},
		{"skill self", skill, "skills/a/SKILL.md", "---\nname: a\ndescription: d\nrequires: [skill:a]\n---\n", "itself"},
		{"agent ok", agent, "agents/a.md", "---\nname: a\ndescription: d\nrequires: [skill:b]\n---\nbody\n", ""},
		{"agent negated", agent, "agents/a.md", "---\nname: a\ndescription: d\nrequires: [\"!skill:b\"]\n---\n", "negated"},
		{"agent unknown capability", agent, "agents/a.md", "---\nname: a\ndescription: d\nrequires: [agent.teleport]\n---\n", "known capability"},
		{"hook ok", hook, "hooks/a/hook.json", `{"event":"Stop","command":"run.sh","requires":["skill:b"]}`, ""},
		{"hook scalar", hook, "hooks/a/hook.json", `{"event":"Stop","command":"run.sh","requires":"skill:b"}`, "array of strings"},
		{"hook self", hook, "hooks/a/hook.json", `{"event":"Stop","command":"run.sh","requires":["hook:a"]}`, "itself"},
		{"hook duplicate", hook, "hooks/a/hook.json", `{"event":"Stop","command":"run.sh","requires":["skill:b","skill:b"]}`, "duplicated"},
		{"hook script not checked", hook, "hooks/a/run.sh", "requires", ""},
		{"instructions plain", instr, "instructions.md", "# Notes\n", ""},
		{"instructions key", instr, "instructions.md", "---\nrequires: [skill:a]\n---\n# Notes\n", `cannot declare "requires"`},
	}
	for _, c := range cases {
		err := validateArtifactContent(c.info, c.rel, []byte(c.content))
		if c.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: error %v, want %q", c.name, err, c.wantErr)
		}
	}
}

// An mcp descriptor refuses `requires` (unknown field).
func TestValidateArtifactContent_MCPRefusesRequires(t *testing.T) {
	err := validateArtifactContent(artifactPathInfo{Kind: "mcp", Name: "m"}, "mcp/m.json",
		[]byte(`{"type":"http","url":"https://example.com/mcp","requires":["skill:a"]}`))
	if err == nil {
		t.Fatal("accepted")
	}
}
