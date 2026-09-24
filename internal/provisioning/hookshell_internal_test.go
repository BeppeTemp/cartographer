package provisioning

// D267 (#412): Claude Code on Windows runs hook commands through Git Bash,
// which deletes the backslashes of a Windows path, so every hook Cartographer
// registered there never ran. These tests pin the Windows spelling from a unix
// host through the hookHostWindows seam.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// pretendWindows sets the hookHostWindows seam for the duration of a test.
func pretendWindows(t *testing.T, windows bool) {
	t.Helper()
	old := hookHostWindows
	hookHostWindows = windows
	t.Cleanup(func() { hookHostWindows = old })
}

func TestPosixShellHookCommand(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			name: "a drive path gets forward slashes",
			in:   `C:\Users\user\.claude\hooks\cartographer-bootstrap\bootstrap.cmd`,
			want: `C:/Users/user/.claude/hooks/cartographer-bootstrap/bootstrap.cmd`,
		},
		{
			name: "a path with a space stays quoted",
			in:   `"C:\Users\Nome Cognome\.claude\hooks\notify\notify.cmd" --flag`,
			want: `"C:/Users/Nome Cognome/.claude/hooks/notify/notify.cmd" --flag`,
		},
		{
			// The arguments are the hook author's: a backslash there may be a
			// deliberate shell escape.
			name: "arguments are left verbatim",
			in:   `C:\hooks\run.cmd a\b`,
			want: `C:/hooks/run.cmd a\b`,
		},
		{
			name: "a UNC path becomes the form Git Bash reads as UNC",
			in:   `\\host\share\notify.exe`,
			want: `//host/share/notify.exe`,
		},
		{
			name: "a slash path is left alone",
			in:   `"C:/Users/Nome Cognome/x.cmd" --flag`,
			want: `"C:/Users/Nome Cognome/x.cmd" --flag`,
		},
		{
			name: "a bare name with arguments is left alone",
			in:   `jq -r '.a\b' # cartographer-hook: .claude/hooks/x/`,
			want: `jq -r '.a\b' # cartographer-hook: .claude/hooks/x/`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := posixShellHookCommand(c.in, true)
			if got != c.want {
				t.Fatalf("posixShellHookCommand(%q, windows) = %q, want %q", c.in, got, c.want)
			}
			if again := posixShellHookCommand(got, true); again != got {
				t.Errorf("not idempotent: second pass = %q, want %q", again, got)
			}
			if unix := posixShellHookCommand(c.in, false); unix != c.in {
				t.Errorf("posixShellHookCommand(%q, unix) = %q, want it unchanged", c.in, unix)
			}
		})
	}
}

// The composition registerHookSettings and registerOpenCodePlugin write: a
// hook.json command resolved against a Windows hook directory whose path
// contains a space. filepath.Join mixes separators on a unix host and does not
// on Windows; the result is the same on both.
func TestWindowsHookCommandIsWrittenWithForwardSlashes(t *testing.T) {
	dir := `C:\Users\Nome Cognome\.claude\hooks\cartographer-bootstrap`
	got := posixShellHookCommand(resolveHookCommand("./bootstrap.cmd", dir), true)
	want := `"C:/Users/Nome Cognome/.claude/hooks/cartographer-bootstrap/bootstrap.cmd"`
	if got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
	if !commandOwnedBy(got, hookOwnershipMarker(BootstrapHookName)) {
		t.Errorf("commandOwnedBy(%q) = false: the entry would be neither idempotent nor prunable", got)
	}
}

func TestHookCommandProblem(t *testing.T) {
	present := filepath.Join(t.TempDir(), "run.sh")
	if err := os.WriteFile(present, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "gone.sh")
	cases := []struct {
		name    string
		command string
		windows bool
		want    string // substring of the problem, "" for none
	}{
		{name: "an existing file runs", command: present + " --flag"},
		{name: "a missing file does not", command: missing, want: "does not exist"},
		{name: "a quoted missing file does not", command: `"` + missing + `" --flag`, want: "does not exist"},
		{name: "a bare name is PATH's business", command: "jq -r ."},
		{name: "a $VAR path is the shell's business", command: "$HOME/bin/x.sh"},
		{name: "a backslash path on Windows", command: `C:\Users\user\.claude\hooks\x\run.cmd`, windows: true, want: "backslashes"},
		{name: "a backslash in an argument is fine", command: present + ` a\b`, windows: true},
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := hookCommandProblem(c.command, c.windows, exists)
			if c.want == "" && got != "" {
				t.Fatalf("hookCommandProblem(%q) = %q, want none", c.command, got)
			}
			if !strings.Contains(got, c.want) {
				t.Fatalf("hookCommandProblem(%q) = %q, want it to mention %q", c.command, got, c.want)
			}
		})
	}
}

// An install that predates D267 has the backslash command in settings.json.
// status must flag it, and the next sync must replace it — one entry, not the
// old one plus its slash rewrite beside it — leaving the user's own hooks
// alone.
func TestPreD267WindowsBootstrapEntryIsReportedAndRewritten(t *testing.T) {
	pretendWindows(t, true)
	baseDir := t.TempDir()
	lock, err := EnsureBootstrapHook(baseDir, configurator.ProviderClaudeCode, Lock{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if findings := VerifyManaged(lock, configurator.ProviderClaudeCode, baseDir); len(findings) != 0 {
		t.Fatalf("fresh install: findings = %+v, want none", findings)
	}

	const old = `C:\Users\user\.claude\hooks\cartographer-bootstrap\bootstrap.cmd`
	const foreign = `C:\tools\my-own-hook.cmd`
	writeSettings(t, baseDir, map[string]interface{}{
		"model": "keep-me",
		"hooks": map[string]interface{}{
			"SessionStart": []interface{}{
				map[string]interface{}{"hooks": []interface{}{
					map[string]interface{}{"type": "command", "command": old},
				}},
				map[string]interface{}{"hooks": []interface{}{
					map[string]interface{}{"type": "command", "command": foreign},
				}},
			},
		},
	})

	findings := VerifyManaged(lock, configurator.ProviderClaudeCode, baseDir)
	if len(findings) != 1 || findings[0].Reason != DriftUnrunnable || !findings[0].Healable() {
		t.Fatalf("findings = %+v, want one healable %q finding", findings, DriftUnrunnable)
	}
	if !strings.Contains(findings[0].Detail, "backslashes") {
		t.Errorf("detail = %q, want it to say why the command cannot run", findings[0].Detail)
	}

	if lock, err = EnsureBootstrapHook(baseDir, configurator.ProviderClaudeCode, lock, false); err != nil {
		t.Fatal(err)
	}
	var commands []string
	settings := readSettingsMap(t, baseDir)
	for _, g := range settings["hooks"].(map[string]interface{})["SessionStart"].([]interface{}) {
		for _, e := range g.(map[string]interface{})["hooks"].([]interface{}) {
			commands = append(commands, e.(map[string]interface{})["command"].(string))
		}
	}
	if len(commands) != 2 || commands[0] != foreign {
		t.Fatalf("SessionStart commands = %q, want the user's hook then exactly one bootstrap entry", commands)
	}
	if strings.Contains(commands[1], `\`) || !strings.HasSuffix(commands[1], "/.claude/hooks/cartographer-bootstrap/"+bootstrapScriptName) {
		t.Errorf("bootstrap command = %q, want the slash form of the materialized script", commands[1])
	}
	if settings["model"] != "keep-me" {
		t.Errorf("model = %v, want the user's key preserved", settings["model"])
	}
	if findings := VerifyManaged(lock, configurator.ProviderClaudeCode, baseDir); len(findings) != 0 {
		t.Errorf("after the rewrite: findings = %+v, want none", findings)
	}
}

// Host-independent: a registration whose command names a file that is not
// there is reported the same way.
func TestClaudeHookWithAMissingCommandFileIsUnrunnable(t *testing.T) {
	pretendWindows(t, false)
	baseDir := t.TempDir()
	lock, err := EnsureBootstrapHook(baseDir, configurator.ProviderClaudeCode, Lock{}, false)
	if err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(baseDir, ".claude", "hooks", BootstrapHookName, "renamed.sh")
	writeSettings(t, baseDir, map[string]interface{}{
		"hooks": map[string]interface{}{
			"SessionStart": []interface{}{
				map[string]interface{}{"hooks": []interface{}{
					map[string]interface{}{"type": "command", "command": gone},
				}},
			},
		},
	})
	findings := VerifyManaged(lock, configurator.ProviderClaudeCode, baseDir)
	if len(findings) != 1 || findings[0].Reason != DriftUnrunnable || !strings.Contains(findings[0].Detail, "does not exist") {
		t.Fatalf("findings = %+v, want one %q finding naming the missing file", findings, DriftUnrunnable)
	}
}

func writeSettings(t *testing.T, baseDir string, settings map[string]interface{}) {
	t.Helper()
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeSettingsPath(baseDir), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readSettingsMap(t *testing.T, baseDir string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(claudeSettingsPath(baseDir))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
