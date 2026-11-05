package lint

import (
	"strings"
	"testing"
)

func requiresFindingsOf(t *testing.T, check string, files map[string]string) map[string]string {
	t.Helper()
	k := tempKB(t)
	for rel, c := range files {
		writeFile(t, k.Root, rel, c)
	}
	fs, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range fs {
		if f.Check == check {
			got[f.Path] = f.Message
		}
	}
	return got
}

func TestRequiresInvalid(t *testing.T) {
	t.Parallel()
	got := requiresFindingsOf(t, "requires_invalid", map[string]string{
		"skills/ok/SKILL.md":   "---\nname: ok\ndescription: d\nrequires: [agent:x]\n---\nB\n",
		"skills/bad/SKILL.md":  "---\nname: bad\ndescription: d\nrequires: [skill:bad]\n---\nB\n",
		"agents/bad.md":        "---\nname: bad\ndescription: d\nrequires: [mcp:x]\n---\nB\n",
		"hooks/ok/hook.json":   `{"event":"Stop","command":"./r.sh","requires":["skill:a"]}`,
		"hooks/bad/hook.json":  `{"event":"Stop","command":"./r.sh","requires":"skill:a"}`,
		"hooks/self/hook.json": `{"event":"Stop","command":"./r.sh","requires":["hook:self"]}`,
		"hooks/null/hook.json": `{"event":"Stop","command":"./r.sh","requires":null}`,
		"hooks/junk/hook.json": `{"event":`,
		"mcp/s.json":           `{"type":"http","url":"https://example.com/mcp","requires":["skill:a"]}`,
		"instructions.md":      "---\nrequires: [skill:a]\n---\nB\n",
	})
	for _, p := range []string{"skills/bad/SKILL.md", "agents/bad.md", "hooks/bad/hook.json", "hooks/self/hook.json", "hooks/null/hook.json", "mcp/s.json", "instructions.md"} {
		if _, ok := got[p]; !ok {
			t.Errorf("%s: want requires_invalid, got %v", p, got)
		}
	}
	for _, p := range []string{"skills/ok/SKILL.md", "hooks/ok/hook.json", "hooks/junk/hook.json"} {
		if m, ok := got[p]; ok {
			t.Errorf("%s must not be reported: %s", p, m)
		}
	}
	if !strings.Contains(got["hooks/self/hook.json"], "still registered") || !strings.Contains(got["hooks/self/hook.json"], "itself") {
		t.Errorf("hook message: %q", got["hooks/self/hook.json"])
	}
	if !strings.Contains(got["agents/bad.md"], "agent") || !strings.Contains(got["skills/bad/SKILL.md"], "skill") {
		t.Errorf("message must name the kind: %v", got)
	}
}

func TestDegradedNameCollision(t *testing.T) {
	t.Parallel()
	got := requiresFindingsOf(t, "degraded_name_collision", map[string]string{
		"agents/twin.md":        "---\nname: twin\ndescription: d\n---\nB\n",
		"skills/twin/SKILL.md":  "---\nname: twin\ndescription: d\n---\nB\n",
		"agents/solo.md":        "---\nname: solo\ndescription: d\n---\nB\n",
		"skills/other/SKILL.md": "---\nname: other\ndescription: d\n---\nB\n",
	})
	if len(got) != 1 || got["agents/twin.md"] == "" {
		t.Errorf("want one finding on agents/twin.md, got %v", got)
	}
}

// Malformed hook.json is hook_invalid's finding alone, never a second one.
func TestRequiresInvalidSkipsMalformedHookJSON(t *testing.T) {
	t.Parallel()
	files := map[string]string{"hooks/junk/hook.json": `{"event":`}
	if got := requiresFindingsOf(t, "requires_invalid", files); len(got) != 0 {
		t.Errorf("requires_invalid on malformed JSON: %v", got)
	}
	if got := requiresFindingsOf(t, "hook_invalid", files); got["hooks/junk/hook.json"] == "" {
		t.Errorf("want hook_invalid, got %v", got)
	}
}
