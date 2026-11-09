package provisioning

// Hook commands are shell-safe, the OpenCode 1.x plugin text is escaped, and the
// removers leave a file that does not name their hook alone (D1620 family: the
// registrars build text for a target syntax, so each value is encoded for it).

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

func TestResolveHookCommand_POSIXQuotingRunsTheIntendedFile(t *testing.T) {
	if hookHostWindows {
		t.Skip("single-quote rule is for POSIX hosts")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	for _, name := range []string{"it's", "rock&roll", "a $HOME b", "back`tick`", `dq"x`} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "ran")
			script := filepath.Join(dir, "run.sh")
			if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$1\" > '"+out+"'\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			got := resolveHookCommand("./run.sh --flag", dir)
			if err := exec.Command("sh", "-n", "-c", got).Run(); err != nil {
				t.Fatalf("sh -n rejects %q: %v", got, err)
			}
			if b, err := exec.Command("sh", "-c", got).CombinedOutput(); err != nil {
				t.Fatalf("sh -c %q: %v\n%s", got, err, b)
			}
			if b, _ := os.ReadFile(out); strings.TrimSpace(string(b)) != "--flag" {
				t.Fatalf("the intended file did not run with its argument: %q", b)
			}
			if again := resolveHookCommand(got, dir); again != got {
				t.Fatalf("not idempotent: %q then %q", got, again)
			}
			if p := hookCommandProblem(got, false, fileExists); p != "" {
				t.Fatalf("doctor flags a working command: %s", p)
			}
		})
	}
}

func TestResolveHookCommand_OldDoubleQuotedFormIsReRegisteredAsSingle(t *testing.T) {
	if hookHostWindows {
		t.Skip("POSIX hosts only")
	}
	got := resolveHookCommand(`"/tmp/a b/run.sh" --x`, "/unused")
	if got != `'/tmp/a b/run.sh' --x` {
		t.Fatalf("got %q", got)
	}
}

func TestOpenCodePlugin1_CommandIsBoundToAConst(t *testing.T) {
	cmds := []string{"${CLAUDE_PROJECT_DIR}/x.sh", "echo `date` >> /tmp/log", "a && b"}
	for _, cmd := range cmds {
		for _, hook := range []string{"SessionStart", "PreToolUse"} {
			mapping := openCodeHookEvents[hook]
			src := generateOpenCodePlugin("h", hookSpec{Event: hook}, mapping, cmd, 1)
			if strings.Contains(src, "$`sh -c "+mustJSONString(cmd)) {
				t.Fatalf("command pasted into the template literal:\n%s", src)
			}
			if !strings.Contains(src, "const command = "+mustJSONString(cmd)+"\n") || !strings.Contains(src, "`sh -c ${command}`") {
				t.Fatalf("the 2.x form is missing for %q:\n%s", cmd, src)
			}
			if strings.Contains(src, `\u003e`) || strings.Contains(src, `\u0026`) {
				t.Fatalf("HTML-escaped literal in:\n%s", src)
			}
			if node, err := exec.LookPath("node"); err == nil {
				f := filepath.Join(t.TempDir(), "plugin.mjs")
				if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
				if b, err := exec.Command(node, "--check", f).CombinedOutput(); err != nil {
					t.Fatalf("node --check: %v\n%s\n%s", err, b, src)
				}
			}
		}
	}
}

func TestRemovers_LeaveAFileThatDoesNotNameTheirHookAlone(t *testing.T) {
	const bad = "{ // not ours\n \"a\": 1,\n}\n"
	write := func(t *testing.T, p, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base := t.TempDir()
	write(t, codexHooksPath(base), bad)
	write(t, kiroHooksPath(base), bad)
	write(t, crushSettingsPath(base, ".config/crush/hooks/h/hook.json"), bad)
	hookRel := ".agents/hooks/h/hook.json"
	write(t, antigravityHooksPath(base, hookRel), bad)

	if err := removeCodexHook(base, "h"); err != nil {
		t.Errorf("codex: %v", err)
	}
	if err := removeKiroHook(base, "h"); err != nil {
		t.Errorf("kiro: %v", err)
	}
	if err := removeCrushHook(base, "h", ".config/crush/hooks/h/hook.json"); err != nil {
		t.Errorf("crush: %v", err)
	}
	if err := removeAntigravityHook(base, "h", hookRel); err != nil {
		t.Errorf("antigravity: %v", err)
	}

	// The same file naming the hook is still an error: it cannot be edited safely.
	bad2 := "{ // not ours\n \"cartographer-h\": 1,\n}\n"
	write(t, codexHooksPath(base), bad2)
	write(t, kiroHooksPath(base), bad2)
	write(t, crushSettingsPath(base, ".config/crush/hooks/h/hook.json"), bad2)
	write(t, antigravityHooksPath(base, hookRel), bad2)
	if removeCodexHook(base, "h") == nil || removeKiroHook(base, "h") == nil ||
		removeCrushHook(base, "h", ".config/crush/hooks/h/hook.json") == nil ||
		removeAntigravityHook(base, "h", hookRel) == nil {
		t.Error("a malformed file that names the hook was accepted")
	}
}

func TestUnknownHookEvent_NamesTheRegistrarsThatRegisterIt(t *testing.T) {
	msg := UnknownHookEvent([]byte(`{"event":"Notificatoin","command":"x.sh"}`))
	for _, p := range []string{"claude", "codex", "copilot", "crush", "kiro", "pi"} {
		if !strings.Contains(msg, p) {
			t.Errorf("message does not name %s: %s", p, msg)
		}
	}
	// What the message claims must be true of the registrars.
	for _, p := range []configurator.Provider{configurator.ProviderCrush, configurator.ProviderKiro, configurator.ProviderCopilot, configurator.ProviderPi, configurator.ProviderCodex, configurator.ProviderClaudeCode} {
		if w, skip := hookEventWarning("h", p, "Notificatoin"); w == "" || skip {
			t.Errorf("%s: unknown event = (%q, %v), want a warning and no skip", p, w, skip)
		}
	}
}

func TestCheckWorkspaceHygiene_OpenCodeJSONIsOwnedAGENTSMdIsShared(t *testing.T) {
	owned := []string{"opencode.json", "AGENTS.md"}
	shared := []string{"AGENTS.md"}
	withTracked(t, map[string]bool{"opencode.json": true})
	if err := CheckWorkspaceHygiene("opencode", t.TempDir(), owned, shared); err == nil {
		t.Error("a tracked opencode.json was accepted")
	}
	withTracked(t, map[string]bool{"AGENTS.md": true})
	if err := CheckWorkspaceHygiene("opencode", t.TempDir(), owned, shared); err != nil {
		t.Errorf("a tracked AGENTS.md was refused: %v", err)
	}
}

func TestRegisterHook_UnknownEventReturnsTheWarning(t *testing.T) {
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "hook.json"), []byte(`{"event":"Notificatoin","command":"./x.sh"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, reg := range map[string]func(string) (string, bool, error){
		"crush": func(base string) (string, bool, error) { return registerCrushHook(base, "h", dest) },
		"kiro":  func(base string) (string, bool, error) { return registerKiroHook(base, "h", dest) },
	} {
		w, ok, err := reg(t.TempDir())
		if err != nil || !ok || !strings.Contains(w, "not a known hook event") {
			t.Errorf("%s: warning %q registered %v err %v", name, w, ok, err)
		}
	}
}

func TestResolveHookCommand_NonSyntaxCharactersStayBare(t *testing.T) {
	if hookHostWindows {
		t.Skip("POSIX hosts only")
	}
	for _, dir := range []string{"/home/user/caffè/h", "/home/user/a~b/h", `/tmp/x\y/h`} {
		if got := resolveHookCommand("./run.sh", dir); got != filepath.Join(dir, "run.sh") && !strings.Contains(dir, `\`) {
			t.Errorf("%q changed form: %q", dir, got)
		}
	}
}

func TestRegisterCopilotHook_PowerShellQuoting(t *testing.T) {
	if hookHostWindows {
		t.Skip("POSIX hosts only")
	}
	dest := filepath.Join(t.TempDir(), "it's a dir")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "hook.json"), []byte(`{"event":"SessionStart","command":"./h.sh --flag"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	rel, _, ok, err := registerCopilotHook(base, "h", dest)
	if err != nil || !ok {
		t.Fatalf("register: %v %v", ok, err)
	}
	data, _ := os.ReadFile(filepath.Join(base, filepath.FromSlash(rel)))
	var doc struct {
		Hooks map[string][]struct{ Bash, Powershell string } `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	e := doc.Hooks["sessionStart"][0]
	want := "& '" + strings.ReplaceAll(filepath.Join(dest, "h.sh"), "'", "''") + "' --flag"
	if e.Powershell != want {
		t.Errorf("powershell = %q, want %q", e.Powershell, want)
	}
	if !strings.HasPrefix(e.Bash, "'") || !strings.Contains(e.Bash, `'\''`) {
		t.Errorf("bash = %q, want the POSIX single-quote form", e.Bash)
	}
}

func TestOpenCodeWriteFindings_ProbeAnswering2ThenFailingKeepsTheShape(t *testing.T) {
	calls := 0
	prev := openCodeVersionProbe
	openCodeVersionProbe = func() (string, error) {
		calls++
		if calls == 1 {
			return "2.0.25", nil
		}
		return "", os.ErrNotExist
	}
	defer func() { openCodeVersionProbe = prev }()

	if !SupportsWriteFindingsHook(configurator.ProviderOpenCode) {
		t.Fatal("first probe says 2.x: supported")
	}
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "hook.json"), []byte(`{"event":"PostToolUse","command":"./h.sh"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	rel, _, ok, err := registerOpenCodePlugin(base, WriteFindingsHookName, dest, ScopeGlobal)
	if err != nil || !ok {
		t.Fatalf("register: %v %v", ok, err)
	}
	data, _ := os.ReadFile(filepath.Join(base, filepath.FromSlash(rel)))
	if !strings.Contains(string(data), "execute.after") || !strings.Contains(string(data), "writeTools") {
		t.Errorf("the generic plugin was written instead of the write-findings one:\n%s", data)
	}
}
