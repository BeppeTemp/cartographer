package provisioning_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// usageHome builds a fake home with a lockfile that manages the given skills
// for the given provider and returns both.
func usageHome(t *testing.T, provider, skillsDir string, names ...string) (string, provisioning.LockFile) {
	t.Helper()
	home := t.TempDir()
	lock := provisioning.Lock{Provider: provider}
	for _, n := range names {
		lock.Managed = append(lock.Managed, provisioning.ManagedFile{
			Kind: "skill", Name: n, Source: "kb:kb-a",
			Path: filepath.ToSlash(filepath.Join(skillsDir, n, "SKILL.md")),
		})
	}
	return home, provisioning.LockFile{Providers: map[string]provisioning.Lock{provider: lock}}
}

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ts(ago time.Duration) string { return time.Now().Add(-ago).UTC().Format(time.RFC3339Nano) }

func claudeLine(t *testing.T, at, typ string, blocks ...map[string]any) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"type": typ, "timestamp": at,
		"message": map[string]any{"role": typ, "content": blocks},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func toolUse(name string, input map[string]any) map[string]any {
	return map[string]any{"type": "tool_use", "name": name, "input": input}
}

func TestScanUsage_Claude_SkillTool(t *testing.T) {
	home, lf := usageHome(t, "claude", ".claude/skills", "ops-tool", "idle-tool")
	writeLines(t, filepath.Join(home, ".claude/projects/p1/s1.jsonl"),
		claudeLine(t, ts(48*time.Hour), "assistant", toolUse("Skill", map[string]any{"skill": "ops-tool"})))
	got, err := provisioning.ScanUsage(lf, home, 90*24*time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	u, ok := got[provisioning.UsageKey("skill", "ops-tool")]
	if !ok || u.Count != 1 || u.Provider != "claude" || u.Source != "kb:kb-a" {
		t.Fatalf("ops-tool usage = %+v (ok=%v)", u, ok)
	}
	if age := time.Since(u.LastUsed); age < 47*time.Hour || age > 49*time.Hour {
		t.Errorf("LastUsed age = %v, want about 48h", age)
	}
	if _, ok := got[provisioning.UsageKey("skill", "idle-tool")]; ok {
		t.Error("a skill never seen must be absent")
	}
}

func TestScanUsage_Claude_ReadOfSkillPath(t *testing.T) {
	home, lf := usageHome(t, "claude", ".claude/skills", "ops-tool")
	path := filepath.Join(home, ".claude/skills/ops-tool/SKILL.md")
	writeLines(t, filepath.Join(home, ".claude/projects/p1/s1.jsonl"),
		claudeLine(t, ts(time.Hour), "assistant", toolUse("Read", map[string]any{"file_path": path})),
		claudeLine(t, ts(time.Hour), "assistant", toolUse("Bash", map[string]any{"command": "cat " + path})),
		// Writing the file is editing the skill, not using it.
		claudeLine(t, ts(time.Hour), "assistant", toolUse("Edit", map[string]any{"file_path": path})))
	got, _ := provisioning.ScanUsage(lf, home, 90*24*time.Hour, true)
	if u := got[provisioning.UsageKey("skill", "ops-tool")]; u.Count != 2 {
		t.Fatalf("Count = %d, want 2 (a Read and a shell cat; the Edit does not count)", u.Count)
	}
}

func TestScanUsage_PathInShellCommand(t *testing.T) {
	for _, provider := range []string{"claude", "codex", "kiro"} {
		t.Run(provider, func(t *testing.T) {
			dir := "." + provider + "/skills"
			home, lf := usageHome(t, provider, dir, "ops-tool")
			path := filepath.Join(home, dir, "ops-tool", "SKILL.md")
			switch provider {
			case "claude":
				writeLines(t, filepath.Join(home, ".claude/projects/p/s.jsonl"),
					claudeLine(t, ts(time.Hour), "assistant", toolUse("Bash", map[string]any{"command": "sed -n 1,40p " + path})))
			case "codex":
				args, _ := json.Marshal(map[string]any{"command": []string{"bash", "-lc", "cat " + path}})
				line, _ := json.Marshal(map[string]any{"timestamp": ts(time.Hour), "type": "response_item",
					"payload": map[string]any{"type": "function_call", "name": "shell", "arguments": string(args)}})
				writeLines(t, filepath.Join(home, ".codex/sessions/2026/10/01/rollout-1-a.jsonl"), string(line))
			case "kiro":
				line, _ := json.Marshal(map[string]any{"timestamp": ts(time.Hour),
					"kind": map[string]any{"BuiltIn": map[string]any{"ExecuteBash": map[string]any{"command": "cat " + path}}}})
				writeLines(t, filepath.Join(home, ".kiro/sessions/h/s.jsonl"), string(line))
			}
			got, _ := provisioning.ScanUsage(lf, home, 90*24*time.Hour, true)
			if u := got[provisioning.UsageKey("skill", "ops-tool")]; u.Count != 1 || u.Provider != provider {
				t.Fatalf("usage = %+v, want one use by %s", u, provider)
			}
		})
	}
}

func TestScanUsage_Claude_IgnoresUserMessages(t *testing.T) {
	home, lf := usageHome(t, "claude", ".claude/skills", "ops-tool")
	path := filepath.Join(home, ".claude/skills/ops-tool/SKILL.md")
	user, _ := json.Marshal(map[string]any{"type": "user", "timestamp": ts(time.Hour),
		"message": map[string]any{"role": "user", "content": "use the ops-tool skill, see " + path}})
	prose, _ := json.Marshal(map[string]any{"type": "assistant", "timestamp": ts(time.Hour),
		"message": map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": "I will read " + path + " and run the Skill ops-tool"}}}})
	// A user-typed record that merely LOOKS like a tool call must not count.
	fake := strings.Replace(claudeLine(t, ts(time.Hour), "assistant", toolUse("Skill", map[string]any{"skill": "ops-tool"})), `"type":"assistant"`, `"type":"user"`, 1)
	writeLines(t, filepath.Join(home, ".claude/projects/p/s.jsonl"), string(user), string(prose), fake)
	got, _ := provisioning.ScanUsage(lf, home, 90*24*time.Hour, true)
	if len(got) != 0 {
		t.Fatalf("usage = %+v, want none: only assistant tool_use blocks count", got)
	}
}

func TestScanUsage_Claude_TolerantOfGarbage(t *testing.T) {
	home, lf := usageHome(t, "claude", ".claude/skills", "ops-tool")
	writeLines(t, filepath.Join(home, ".claude/projects/p/s.jsonl"),
		`not json at all "tool_use"`, `{"type":"assistant","message":{"content":"a string"}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use"}]},"extra":{"x":1}}`,
		claudeLine(t, ts(time.Hour), "assistant", toolUse("Skill", map[string]any{"skill": "ops-tool"})))
	got, err := provisioning.ScanUsage(lf, home, 90*24*time.Hour, true)
	if err != nil || got[provisioning.UsageKey("skill", "ops-tool")].Count != 1 {
		t.Fatalf("got %+v, err %v: malformed lines must be skipped, the good one counted", got, err)
	}
}

func TestScanUsage_Window(t *testing.T) {
	home, lf := usageHome(t, "claude", ".claude/skills", "ops-tool")
	writeLines(t, filepath.Join(home, ".claude/projects/p/s.jsonl"),
		claudeLine(t, ts(100*24*time.Hour), "assistant", toolUse("Skill", map[string]any{"skill": "ops-tool"})))
	got, _ := provisioning.ScanUsage(lf, home, 90*24*time.Hour, true)
	if len(got) != 0 {
		t.Fatalf("an event older than the window counted: %+v", got)
	}
}

func TestScanUsage_Codex_CatalogLoaded(t *testing.T) {
	home, lf := usageHome(t, "codex", ".codex/skills", "ops-tool", "idle-tool")
	at := ts(72 * time.Hour)
	ws, _ := json.Marshal(map[string]any{"timestamp": at, "type": "world_state",
		"payload": map[string]any{"state": map[string]any{"host_skills": []string{"ops-tool", "idle-tool"}}}})
	empty, _ := json.Marshal(map[string]any{"timestamp": ts(time.Hour), "type": "world_state",
		"payload": map[string]any{"state": map[string]any{"host_skills": []string{}}}})
	writeLines(t, filepath.Join(home, ".codex/sessions/2026/10/01/rollout-1-a.jsonl"), string(ws), string(empty))
	got, _ := provisioning.ScanUsage(lf, home, 90*24*time.Hour, true)
	for _, n := range []string{"ops-tool", "idle-tool"} {
		u, ok := got[provisioning.UsageKey("skill", n)]
		if !ok || u.Count != 0 || u.Provider != "codex" || time.Since(u.LastUsed) < 71*time.Hour || time.Since(u.LastUsed) > 73*time.Hour {
			t.Errorf("%s = %+v (ok=%v), want a catalogue timestamp 72h old and Count 0", n, u, ok)
		}
	}
}

func TestScanUsage_Codex_IgnoresMessages(t *testing.T) {
	home, lf := usageHome(t, "codex", ".codex/skills", "ops-tool")
	path := filepath.Join(home, ".codex/skills/ops-tool/SKILL.md")
	msg, _ := json.Marshal(map[string]any{"timestamp": ts(time.Hour), "type": "response_item",
		"payload": map[string]any{"type": "message", "role": "user", "arguments": path, "content": path}})
	writeLines(t, filepath.Join(home, ".codex/sessions/2026/10/01/rollout-1-a.jsonl"), string(msg))
	if got, _ := provisioning.ScanUsage(lf, home, 90*24*time.Hour, true); len(got) != 0 {
		t.Fatalf("a message item counted: %+v", got)
	}
}

// The Kiro record shape comes from testdata/usage/kiro-session.jsonl: a
// redacted record (home replaced by $HOME) of the shape docs/harnesses.md
// §kiro declares.
func TestScanUsage_Kiro_FileRead(t *testing.T) {
	home, lf := usageHome(t, "kiro", ".kiro/skills", "ops-tool", "other-tool", "never-used")
	raw, err := os.ReadFile("testdata/usage/kiro-session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.NewReplacer("$HOME", filepath.ToSlash(home), "__NOW__", ts(2*time.Hour)).Replace(string(raw))
	if err := os.MkdirAll(filepath.Join(home, ".kiro/sessions/h"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".kiro/sessions/h/s.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := provisioning.ScanUsage(lf, home, 90*24*time.Hour, true)
	if u := got[provisioning.UsageKey("skill", "ops-tool")]; u.Count != 1 || u.Provider != "kiro" {
		t.Errorf("ops-tool = %+v, want one FileRead use", u)
	}
	if u := got[provisioning.UsageKey("skill", "other-tool")]; u.Count != 1 {
		t.Errorf("other-tool = %+v, want its shell cat counted", u)
	}
	if _, ok := got[provisioning.UsageKey("skill", "never-used")]; ok {
		t.Error("a path in message text counted; only tool records may")
	}
	if len(got) != 2 {
		t.Errorf("a read of a non-skill path produced usage: %+v", got)
	}
}

func TestScanUsage_UnsupportedProvidersContributeNothing(t *testing.T) {
	for _, p := range []string{"opencode", "hermes", "antigravity", "crush"} {
		home, lf := usageHome(t, p, "."+p+"/skills", "ops-tool")
		got, err := provisioning.ScanUsage(lf, home, 90*24*time.Hour, true)
		if err != nil || len(got) != 0 {
			t.Errorf("%s: got %+v, err %v, want empty", p, got, err)
		}
	}
	_, _, unsupported := provisioning.UsageProviders()
	if len(unsupported) != 4 {
		t.Errorf("unsupported = %v", unsupported)
	}
}

func TestScanUsage_OptOut(t *testing.T) {
	home, lf := usageHome(t, "claude", ".claude/skills", "ops-tool")
	writeLines(t, filepath.Join(home, ".claude/projects/p/s.jsonl"),
		claudeLine(t, ts(time.Hour), "assistant", toolUse("Skill", map[string]any{"skill": "ops-tool"})))
	got, err := provisioning.ScanUsage(lf, home, 90*24*time.Hour, false)
	if err != nil || got != nil {
		t.Fatalf("got %+v, err %v, want nil: the opt-out reads nothing", got, err)
	}
}

func TestScanUsage_EmptyDir(t *testing.T) {
	home, lf := usageHome(t, "claude", ".claude/skills", "ops-tool")
	got, err := provisioning.ScanUsage(lf, home, 90*24*time.Hour, true)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v, err %v, want an empty map", got, err)
	}
}

// D148: the scanner is read-only on client directories.
func TestScanUsage_WritesNothing(t *testing.T) {
	home, lf := usageHome(t, "claude", ".claude/skills", "ops-tool")
	writeLines(t, filepath.Join(home, ".claude/projects/p/s.jsonl"),
		claudeLine(t, ts(time.Hour), "assistant", toolUse("Skill", map[string]any{"skill": "ops-tool"})))
	before := snapshotTree(t, home)
	_, _ = provisioning.ScanUsage(lf, home, 90*24*time.Hour, true)
	if after := snapshotTree(t, home); after != before {
		t.Fatalf("the scan changed the client directory:\n%s\n--\n%s", before, after)
	}
}

func snapshotTree(t *testing.T, root string) string {
	t.Helper()
	var sb strings.Builder
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil {
			sb.WriteString(p + " " + info.ModTime().String() + "\n")
		}
		return nil
	})
	return sb.String()
}
