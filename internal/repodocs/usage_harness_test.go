package repodocs

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// test/usage/run.sh is the usage test: a headless agent through a user's tasks, the verdict from
// assertions. These tests run it with a fake agent that replays canned transcripts, so no network
// and no model is involved. They skip on Windows, like the other script tests.

// fakeAgent is the stand-in for the agent CLI. FAKE_MODE picks what it replays: ok, unknown (a call
// to a tool the server did not list), breach (a second MCP server in the session), down (the model
// is overloaded), skill (a skill in the session), plugin (a plugin that is not the CLI's own) and hook
// (a hook on an event other than SessionStart).
const fakeAgent = `#!/bin/sh
[ "$1" = "--version" ] && { echo "fake-agent 1.0"; exit 0; }
mode=${FAKE_MODE:-ok}
servers='[{"name":"kb","status":"connected"}]'
tools='["mcp__kb__lint"]'
skills='[]'
plugins='[]'
[ "$mode" = skill ] && skills='["my-skill"]'
[ "$mode" = plugin ] && plugins='[{"name":"user-plugin","path":"/home/user/.claude/plugins/x"},{"name":"builtin-one","path":"builtin"}]'
case " $* " in *" --include-hook-events "*) hooks=1 ;; *) hooks= ;; esac
if [ "$mode" = hook ] && [ -n "$hooks" ]; then
  printf '{"type":"system","subtype":"hook_started","hook_name":"PreToolUse:x","hook_event":"PreToolUse"}\n'
fi
[ "$mode" = breach ] && servers='[{"name":"kb","status":"connected"},{"name":"other","status":"connected"}]'
printf '{"type":"system","subtype":"init","mcp_servers":%s,"tools":%s,"plugins":%s,"skills":%s,"slash_commands":[]}\n' "$servers" "$tools" "$plugins" "$skills"
if [ "$mode" = down ]; then
  printf '{"type":"result","is_error":true,"result":"API Error: 529 overloaded","num_turns":1,"usage":{}}\n'
  exit 1
fi
# the harness must pass the turn ceiling, and keep no session on disk
case " $* " in *" --max-turns "*) ;; *) echo "no --max-turns" >&2; exit 3 ;; esac
case " $* " in *" --no-session-persistence "*) ;; *) echo "no --no-session-persistence" >&2; exit 3 ;; esac
case " $* " in *" --include-hook-events "*) ;; *) echo "no --include-hook-events: a hook would go unseen" >&2; exit 3 ;; esac
case " $* " in *" --disable-slash-commands "*) ;; *) echo "no --disable-slash-commands" >&2; exit 3 ;; esac
name=mcp__kb__lint
[ "$mode" = unknown ] && [ "$USAGE_TASK" != isolation ] && name=mcp__kb__nonexistent
printf '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"%s","input":{}}]}}\n' "$name"
printf '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"ok"}]}}\n'
printf '{"type":"result","is_error":false,"result":"done","num_turns":2,"usage":{"input_tokens":10,"output_tokens":5}}\n'
`

var (
	usageBinOnce sync.Once
	usageBin     string
	usageBinErr  error
)

// usageBinary builds the binary the harness makes its KBs with, once for all the tests.
func usageBinary(t *testing.T) string {
	t.Helper()
	usageBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "usage-bin")
		if err != nil {
			usageBinErr = err
			return
		}
		usageBin = filepath.Join(dir, "cartographer")
		cmd := exec.Command("go", "build", "-o", usageBin, "./cmd/cartographer")
		cmd.Dir = repoRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			usageBinErr = &buildError{string(out)}
		}
	})
	if usageBinErr != nil {
		t.Fatalf("building the binary: %v", usageBinErr)
	}
	return usageBin
}

type buildError struct{ out string }

func (e *buildError) Error() string { return e.out }

// usageRun runs the harness with the fake agent over a tasks directory holding one task whose
// check is the given shell body; it returns the combined output and the exit code.
func usageRun(t *testing.T, mode, check string, extra ...string) (string, int) {
	t.Helper()
	bash := bashOrSkip(t)
	if runtime.GOOS == "windows" {
		t.Skip("the harness is a unix script")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := usageBinary(t)
	dir := t.TempDir()
	agent := filepath.Join(dir, "fake-agent")
	tasks := filepath.Join(dir, "tasks")
	for p, body := range map[string]string{
		agent:                                fakeAgent,
		filepath.Join(tasks, "01_sample.md"): "---\nturns: 5\ntool-errors: 1\nseed:\n---\nDo the sample thing.\n",
		filepath.Join(tasks, "01_sample.check.sh"): "#!/usr/bin/env bash\n" + check + "\n",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tmp := t.TempDir()
	cmd := exec.Command(bash, "test/usage/run.sh")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(append(os.Environ(),
		"BIN="+bin, "USAGE_AGENT_CMD="+agent, "USAGE_TASKS_DIR="+tasks, "FAKE_MODE="+mode, "TMPDIR="+tmp, "TASKS=", "MODEL=claude-fake-1-0", "GITHUB_STEP_SUMMARY=/dev/null"),
		extra...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running the harness: %v", err)
	}
	return string(out), code
}

// AC-P4-11: with the fake agent a passing task, an unknown tool, an isolation breach and an
// unavailable model give exit 0, 1, 1 and 75, and the output names the task and the reason.
func TestUsageHarnessVerdicts(t *testing.T) {
	for _, tc := range []struct {
		name, mode, check string
		code              int
		want              []string
	}{
		{"a passing task", "ok", "exit 0", 0, []string{"PASS isolation", "PASS 01_sample", "calls=1", "tokens_in=10", "fake-agent 1.0"}},
		{"a tool the server did not list", "unknown", "exit 0", 1, []string{"FAIL 01_sample", "called a tool the server did not list: mcp__kb__nonexistent"}},
		{"a failing check", "ok", "echo the page is missing; exit 1", 1, []string{"FAIL 01_sample", "check: the page is missing"}},
		{"an isolation breach", "breach", "exit 0", 1, []string{"FAIL isolation", "MCP servers 'kb,other'"}},
		{"a skill in the session", "skill", "exit 0", 1, []string{"FAIL isolation", "skills or commands: my-skill"}},
		{"a plugin that is not the CLI's own", "plugin", "exit 0", 1, []string{"FAIL isolation", "loaded plugins: user-plugin"}},
		{"a hook on another event", "hook", "exit 0", 1, []string{"FAIL isolation", "ran hooks: PreToolUse:x"}},
		{"an unavailable model", "down", "exit 0", 75, []string{"NOT RUN", "unavailable"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code := usageRun(t, tc.mode, tc.check)
			if code != tc.code {
				t.Errorf("exit %d, want %d\n%s", code, tc.code, out)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q\n%s", w, out)
				}
			}
			if tc.mode != "ok" && tc.mode != "unknown" && tc.mode != "down" && strings.Contains(out, "01_sample") {
				t.Errorf("an isolation breach fails the run before a task is paid for\n%s", out)
			}
		})
	}
}

// AC-P4-12: no command of the harness reads or writes outside the scratch directory: with HOME on a
// read-only directory, the run passes, leaves that directory empty and removes its scratch.
func TestUsageHarnessStaysInItsScratchDirectory(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("a read-only directory stops nobody as root")
	}
	home := filepath.Join(t.TempDir(), "ro-home")
	if err := os.Mkdir(home, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o755) })
	out, code := usageRun(t, "ok", "exit 0", "HOME="+home, "XDG_CONFIG_HOME=", "XDG_STATE_HOME=", "XDG_DATA_HOME=", "XDG_CACHE_HOME=")
	if code != 0 {
		t.Fatalf("exit %d with a read-only HOME\n%s", code, out)
	}
	if ents, _ := os.ReadDir(home); len(ents) != 0 {
		t.Errorf("the harness wrote into HOME: %v", ents)
	}
	if strings.Contains(out, "scratch kept") {
		t.Errorf("a pass removes its scratch directory unless KEEP=1\n%s", out)
	}
}

// AC-P4-24: no workflow calls the harness, and none starts an agent CLI: the usage test runs in a
// maintainer's own agent session (no CI secret, no recurring cost).
func TestNoWorkflowCallsTheUsageTest(t *testing.T) {
	root := repoRoot(t)
	var files []string
	err := filepath.WalkDir(filepath.Join(root, ".github"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return err
	})
	if err != nil || len(files) == 0 {
		t.Fatalf("no file under .github/: the check is not doing anything (%v)", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, banned := range []string{"usage-test", "test/usage"} {
			if strings.Contains(string(b), banned) {
				rel, _ := filepath.Rel(root, f)
				t.Errorf("%s names %q: the usage test is run by a maintainer's coordinator, never by a workflow", rel, banned)
			}
		}
	}
}

// A wrong invocation is exit 2, with the reason: a spend cap that is no number is refused before anything runs.
func TestUsageHarnessRefusesABadBudget(t *testing.T) {
	out, code := usageRun(t, "ok", "exit 0", "USAGE_BUDGET_USD=a lot")
	if code != 2 || !strings.Contains(out, "USAGE_BUDGET_USD must be a number") {
		t.Errorf("exit %d, want 2 with the reason\n%s", code, out)
	}
}
