package provisioning

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// clineBase is a fake home; the SessionStart markers go to a temp dir of the test.
func clineBase(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sh scripts")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	tmp := t.TempDir()
	old := clineTempDir
	clineTempDir = func() string { return tmp }
	t.Cleanup(func() { clineTempDir = old })
	return t.TempDir()
}

// writeClineHook materializes a hook where Apply will (clineHookFilesPrefix).
func writeClineHook(t *testing.T, base, name, event, matcher, script string) {
	t.Helper()
	dir := filepath.Join(base, filepath.FromSlash(clineHookFilesPrefix), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	spec, _ := json.Marshal(hookSpec{Event: event, Matcher: matcher, Command: "./run.sh"})
	if err := os.WriteFile(filepath.Join(dir, "hook.json"), spec, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func dispatch(base, event, payload string) ClineDispatchResult {
	return DispatchClineHooks(base, event, []byte(payload))
}

func TestDispatchClineHooks(t *testing.T) {
	base := clineBase(t)
	out := filepath.Join(t.TempDir(), "payload")
	rec := "#!/bin/sh\ncat > '" + out + "'\n"
	writeClineHook(t, base, "guard", "PreToolUse", "concept_write$", rec+"echo 'no writes' >&2\nexit 2\n")
	writeClineHook(t, base, "other", "PreToolUse", "^Bash$", "#!/bin/sh\nexit 2\n")
	writeClineHook(t, base, "note", "PostToolUse", "", "#!/bin/sh\ncat >/dev/null\necho 'check it' >&2\nexit 2\n")
	writeClineHook(t, base, "boot", "SessionStart", "", "#!/bin/sh\necho 'update available'\n")
	writeClineHook(t, base, "ask", "UserPromptSubmit", "", "#!/bin/sh\necho 'be brief'\n")
	writeClineHook(t, base, "stop", "Stop", "", "#!/bin/sh\necho never\n")

	pre := `{"taskId":"t1","workspaceRoots":["` + filepath.ToSlash(base) + `"],"preToolUse":{"toolName":"srv__concept_write","parameters":{"path":"a.md","tags":"[\"x\"]"}}}`
	res := dispatch(base, "PreToolUse", pre)
	if !res.Cancel || res.ErrorMessage != "no writes" {
		t.Fatalf("PreToolUse exit 2 must cancel with stderr (the matcher of 'other' does not match): %+v", res)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(readFile(t, out)), &p); err != nil {
		t.Fatal(err)
	}
	if p["tool_name"] != "mcp__srv__concept_write" || p["hook_event_name"] != "PreToolUse" || p["session_id"] != "t1" || p["cwd"] != filepath.ToSlash(base) {
		t.Errorf("payload = %v", p)
	}
	if in, _ := p["tool_input"].(map[string]any); in["path"] != "a.md" || len(in["tags"].([]any)) != 1 {
		t.Errorf("tool_input = %v", p["tool_input"])
	}
	// A built-in tool of Cline matches a Claude-shaped matcher by its raw name only.
	if res := dispatch(base, "PreToolUse", `{"preToolUse":{"toolName":"Bash"}}`); !res.Cancel || res.ErrorMessage != "blocked by hook other" {
		t.Errorf("raw name match: %+v", res)
	}

	post := `{"postToolUse":{"toolName":"srv__x","parameters":{},"result":"ok","success":true}}`
	if res := dispatch(base, "PostToolUse", post); res.Cancel || res.ContextModification != "check it" {
		t.Errorf("PostToolUse must be context, never cancel: %+v", res)
	}
	if res := dispatch(base, "PostToolUse", strings.Replace(post, "true", "false", 1)); res != (ClineDispatchResult{}) {
		t.Errorf("a failed tool fires no PostToolUse hook: %+v", res)
	}

	if res := dispatch(base, "UserPromptSubmit", `{"userPromptSubmit":{"prompt":"hi"}}`); res.ContextModification != "be brief" {
		t.Errorf("UserPromptSubmit: %+v", res)
	}
	start := `{"taskId":"task-1","taskStart":{}}`
	if res := dispatch(base, "TaskStart", start); res.ContextModification != "update available" {
		t.Errorf("TaskStart: %+v", res)
	}
	if res := dispatch(base, "TaskStart", start); res != (ClineDispatchResult{}) {
		t.Errorf("a second TaskStart of the same task must be a no-op: %+v", res)
	}
	if res := dispatch(base, "TaskStart", `{"taskId":"task-2"}`); res.ContextModification == "" {
		t.Error("another task starts again")
	}
	if res := dispatch(base, "PreToolUse", "not json"); res != (ClineDispatchResult{}) {
		t.Errorf("garbage: %+v", res)
	}
	if res := dispatch(base, "Stop", `{}`); res != (ClineDispatchResult{}) {
		t.Errorf("an event Cline does not fire: %+v", res)
	}
}

func TestDispatchClineHooksMergesAndStaysInBudget(t *testing.T) {
	base := clineBase(t)
	writeClineHook(t, base, "a", "PostToolUse", "", "#!/bin/sh\necho one >&2\nexit 2\n")
	writeClineHook(t, base, "b", "PostToolUse", "", "#!/bin/sh\necho two >&2\nexit 2\n")
	writeClineHook(t, base, "c", "PostToolUse", "", "#!/bin/sh\nsleep 30\n")
	old := clineHookBudget
	clineHookBudget = 6 * time.Second
	t.Cleanup(func() { clineHookBudget = old })
	start := time.Now()
	res := dispatch(base, "PostToolUse", `{"postToolUse":{"toolName":"x","success":true}}`)
	if res.ContextModification != "one\n\ntwo" {
		t.Errorf("merged = %q (a timeout has no effect)", res.ContextModification)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %v: the budget is not enforced", time.Since(start))
	}
}

func TestClineHooksSkipsInvalidMatchersAndUnreadableSpecs(t *testing.T) {
	base := clineBase(t)
	writeClineHook(t, base, "ok", "PreToolUse", "x", "#!/bin/sh\n")
	for name, spec := range map[string]string{
		"bad-matcher": `{"event":"PreToolUse","matcher":"(","command":"x"}`,
		"bad-json":    `{`,
	} {
		dir := filepath.Join(base, filepath.FromSlash(clineHookFilesPrefix), name)
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "hook.json"), []byte(spec), 0o644)
	}
	if hs := clineHooks(base, ""); len(hs) != 1 || hs[0].name != "ok" {
		t.Errorf("hooks = %+v", hs)
	}
	if hs := clineHooks(base, clineHookFilesPrefix+"ok"); len(hs) != 0 {
		t.Errorf("exclude: %+v", hs)
	}
}

func TestCapRunes(t *testing.T) {
	if got := capRunes(strings.Repeat("è", 60000), clineMaxContext); len([]rune(got)) != clineMaxContext {
		t.Errorf("len = %d", len([]rune(got)))
	}
}

// The hook command is built through the #1632 seam: on Windows the executable
// gets forward slashes, because the shell running it deletes a backslash.
func TestClineHookCommandUsesForwardSlashesOnWindows(t *testing.T) {
	h := clineHook{dir: `C:\Users\user\.cline\cartographer-hooks\g`, spec: hookSpec{Command: "./run.sh --flag"}}
	pretendWindows(t, true)
	if got, want := clineHookCommand(h), `C:/Users/user/.cline/cartographer-hooks/g/run.sh --flag`; got != want {
		t.Errorf("command = %s, want %s", got, want)
	}
}

func TestDispatchClineMatcherIsForToolEventsOnly(t *testing.T) {
	base := clineBase(t)
	writeClineHook(t, base, "ask", "UserPromptSubmit", "^never$", "#!/bin/sh\necho 'be brief'\n")
	if res := dispatch(base, "UserPromptSubmit", `{"userPromptSubmit":{"prompt":"hi"}}`); res.ContextModification != "be brief" {
		t.Errorf("a matcher on UserPromptSubmit must not stop the hook: %+v", res)
	}
}

func TestDispatchClineTotalStaysInBudget(t *testing.T) {
	base := clineBase(t)
	for _, n := range []string{"a", "b", "c"} {
		// the grandchild keeps the pipes open, so the WaitDelay is spent too
		writeClineHook(t, base, n, "PostToolUse", "", "#!/bin/sh\nsleep 30 &\nwait\n")
	}
	ob, od := clineHookBudget, clineWaitDelay
	clineHookBudget, clineWaitDelay = 3*time.Second, 300*time.Millisecond
	t.Cleanup(func() { clineHookBudget, clineWaitDelay = ob, od })
	start := time.Now()
	dispatch(base, "PostToolUse", `{"postToolUse":{"toolName":"x","success":true}}`)
	if d := time.Since(start); d > clineHookBudget+300*time.Millisecond {
		t.Errorf("took %v, over the %v budget", d, clineHookBudget)
	}
}

func TestClineMissingShellKeepsTheTaskStartMarker(t *testing.T) {
	base := clineBase(t)
	writeClineHook(t, base, "boot", "SessionStart", "", "#!/bin/sh\necho hi\n")
	t.Setenv("PATH", "")
	if res := dispatch(base, "TaskStart", `{"taskId":"t"}`); res != (ClineDispatchResult{}) {
		t.Fatalf("no sh: %+v", res)
	}
	t.Setenv("PATH", "/bin:/usr/bin")
	if res := dispatch(base, "TaskStart", `{"taskId":"t"}`); res.ContextModification != "hi" {
		t.Errorf("a missing sh took the marker: %+v", res)
	}
}

func TestClineMarkerDirIsPrivateAndNeverALink(t *testing.T) {
	_ = clineBase(t)
	dir := clineMarkerDir()
	if dir == "" {
		t.Fatal("no marker dir")
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if clineMarkerDir() != "" {
		t.Error("a group-readable marker dir was trusted")
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Fatal(err)
	}
	if clineMarkerDir() != "" {
		t.Error("a symlinked marker dir was trusted")
	}
	if !clineFirstTaskStart("x") || !clineFirstTaskStart("x") {
		t.Error("an untrusted dir must fail open: the sync runs")
	}
}

func TestDispatchClineCapsChildOutput(t *testing.T) {
	base := clineBase(t)
	writeClineHook(t, base, "loud", "SessionStart", "", "#!/bin/sh\nhead -c 3000000 /dev/zero | tr '\\0' x\n")
	res := dispatch(base, "TaskStart", `{"taskId":"big"}`)
	if n := len(res.ContextModification); n == 0 || n > clineMaxContext {
		t.Errorf("context = %d bytes", n)
	}
	w := &capWriter{max: 4}
	if n, _ := w.Write([]byte("abcdefgh")); n != 8 || w.String() != "abcd" {
		t.Errorf("capWriter kept %q, reported %d", w.String(), n)
	}
}
