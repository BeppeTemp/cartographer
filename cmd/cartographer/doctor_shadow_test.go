package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// shadowRepo is a git repository with the given files; tracked ones are added.
func shadowRepo(t *testing.T, tracked []string, untracked []string) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v %s", err, out)
	}
	write := func(rel string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range tracked {
		write(f)
	}
	for _, f := range untracked {
		write(f)
	}
	if len(tracked) > 0 {
		args := append([]string{"-C", dir, "add", "--"}, tracked...)
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git add: %v %s", err, out)
		}
	}
	return dir
}

func shadowFixture(workspaces map[string][]string, arts ...contract.Artifact) (*clientconfig.Config, candidateSet) {
	cfg := &clientconfig.Config{KnownKBs: []string{"kb-a"}, Agents: []string{"claude", "codex"}}
	for provider, paths := range workspaces {
		for _, p := range paths {
			if cfg.Workspaces == nil {
				cfg.Workspaces = map[string][]clientconfig.WorkspaceBinding{}
			}
			cfg.Workspaces[provider] = append(cfg.Workspaces[provider], clientconfig.WorkspaceBinding{Path: p, KBs: []string{"kb-a"}})
		}
	}
	return cfg, candidateSet{Named: map[string][]contract.Artifact{"kb-a": arts}}
}

func kbArt(kind, name string) contract.Artifact {
	return contract.Artifact{Kind: kind, Name: name, Source: "kb:kb-a"}
}

func TestCheckWorkspaceShadows(t *testing.T) {
	t.Run("tracked agent file", func(t *testing.T) {
		ws := shadowRepo(t, []string{".claude/agents/review-mr.md"}, nil)
		cfg, cs := shadowFixture(map[string][]string{"claude": {ws}}, kbArt("agent", "review-mr"))
		got := checkWorkspaceShadows(cfg, []string{"claude"}, cs)
		if len(got) != 1 || got[0].Check != "workspace-shadow" || got[0].Severity != doctorWarning {
			t.Fatalf("got %+v", got)
		}
		for _, want := range []string{"agents/review-mr.md", ".claude/agents/review-mr.md", "kb-a", "tracked by git"} {
			if !strings.Contains(got[0].Message, want) {
				t.Errorf("message %q lacks %q", got[0].Message, want)
			}
		}
		if got[0].Path != filepath.Join(ws, ".claude", "agents", "review-mr.md") || !strings.Contains(got[0].Fix, "artifact_prefix") {
			t.Errorf("path/fix = %q / %q", got[0].Path, got[0].Fix)
		}
	})
	t.Run("untracked agent file", func(t *testing.T) {
		ws := shadowRepo(t, []string{"README.md"}, []string{".claude/agents/review-mr.md"})
		cfg, cs := shadowFixture(map[string][]string{"claude": {ws}}, kbArt("agent", "review-mr"))
		if got := checkWorkspaceShadows(cfg, []string{"claude"}, cs); len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("skill directory", func(t *testing.T) {
		ws := shadowRepo(t, []string{".claude/skills/review-mr/SKILL.md", ".claude/skills/other/SKILL.md"}, nil)
		cfg, cs := shadowFixture(map[string][]string{"claude": {ws}}, kbArt("skill", "review-mr"), kbArt("skill", "deploy"))
		got := checkWorkspaceShadows(cfg, []string{"claude"}, cs)
		if len(got) != 1 || !strings.Contains(got[0].Message, "skill review-mr") || !strings.Contains(got[0].Message, "skills/review-mr/SKILL.md") {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("a file of another name", func(t *testing.T) {
		ws := shadowRepo(t, []string{".claude/agents/other.md", ".claude/skills/review-mr-extra/SKILL.md"}, nil)
		cfg, cs := shadowFixture(map[string][]string{"claude": {ws}}, kbArt("agent", "review-mr"), kbArt("skill", "review-mr"))
		if got := checkWorkspaceShadows(cfg, []string{"claude"}, cs); len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("no project destination", func(t *testing.T) {
		ws := shadowRepo(t, []string{".claude/agents/review-mr.md"}, nil)
		cfg, cs := shadowFixture(map[string][]string{"copilot": {ws}}, kbArt("agent", "review-mr"))
		cfg.Agents = append(cfg.Agents, "copilot")
		if got := checkWorkspaceShadows(cfg, []string{"copilot"}, cs); len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("unbound workspace", func(t *testing.T) {
		ws := shadowRepo(t, []string{".claude/agents/review-mr.md"}, nil)
		_ = ws
		cfg, cs := shadowFixture(nil, kbArt("agent", "review-mr"))
		if got := checkWorkspaceShadows(cfg, []string{"claude"}, cs); len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("two providers sharing a workspace", func(t *testing.T) {
		ws := shadowRepo(t, []string{".claude/agents/review-mr.md", ".codex/agents/review-mr.toml"}, nil)
		cfg, cs := shadowFixture(map[string][]string{"claude": {ws}, "codex": {ws}}, kbArt("agent", "review-mr"))
		got := checkWorkspaceShadows(cfg, []string{"claude", "codex"}, cs)
		if len(got) != 2 {
			t.Fatalf("got %+v", got)
		}
	})
	// Windows forbids 0x01-0x1f in a file name, so the control byte is DEL
	// (0x7f): a control character IsPrint rejects, legal in a Windows name.
	t.Run("control characters are escaped", func(t *testing.T) {
		ws := shadowRepo(t, []string{".claude/agents/a\x7fb.md"}, nil)
		cfg, cs := shadowFixture(map[string][]string{"claude": {ws}}, kbArt("agent", "a\x7fb"))
		got := checkWorkspaceShadows(cfg, []string{"claude"}, cs)
		if len(got) != 1 || strings.ContainsRune(got[0].Message, '\x7f') || !strings.Contains(got[0].Message, `\x7f`) {
			t.Fatalf("got %+q", got)
		}
	})
}

// With no /health answer there is no signal: the candidates are not pulled.
func TestDoctorKBCandidates_SilentWithoutHealth(t *testing.T) {
	cfg := &clientconfig.Config{KnownKBs: []string{"kb-a"}, Agents: []string{"claude"}}
	if _, ok := doctorKBCandidates(cfg, cfg.Agents, serverFacts{}); ok {
		t.Fatal("candidates reported without a health answer")
	}
}

// printDoctorReport escapes a finding's path: a workspace-controlled file name
// must not reach the terminal as a control byte.
func TestPrintDoctorReport_EscapesPath(t *testing.T) {
	out := withStdout(t, func() {
		printDoctorReport(doctorReport{Findings: []doctorFinding{{Check: "workspace-shadow", Severity: doctorWarning, Message: "m", Path: "/ws/.claude/agents/a\x1bb.md"}}})
	})
	if strings.ContainsRune(out, '\x1b') || !strings.Contains(out, "/ws/.claude/agents/a") {
		t.Fatalf("output = %q", out)
	}
}
