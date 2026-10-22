package gitx

import (
	"os"
	"strings"
	"testing"
)

func envValue(env []string, key string) (string, bool) {
	v, ok := "", false
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			v, ok = strings.TrimPrefix(kv, key+"="), true
		}
	}
	return v, ok
}

// The 401 test cannot tell the fix from its absence under go test (no tty),
// so the environment is checked directly (#1004).
func TestGitEnv_NeverPrompts(t *testing.T) {
	if v, _ := envValue(gitEnv(nil), "GIT_TERMINAL_PROMPT"); v != "0" {
		t.Errorf("GIT_TERMINAL_PROMPT = %q, want 0", v)
	}
}

func TestNetEnv_SSHBatchMode(t *testing.T) {
	dir := t.TempDir()
	runIn(t, dir, "init", "-q")
	t.Setenv("GIT_SSH_COMMAND", "")
	os.Unsetenv("GIT_SSH_COMMAND")
	os.Unsetenv("GIT_SSH")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	if v, _ := envValue(netEnv(dir, nil), "GIT_SSH_COMMAND"); !strings.Contains(v, "BatchMode=yes") {
		t.Errorf("nothing set: GIT_SSH_COMMAND = %q, want BatchMode=yes", v)
	}
	if v, ok := envValue(netEnv(dir, []string{"GIT_SSH_COMMAND=ssh -i k"}), "GIT_SSH_COMMAND"); !ok || v != "ssh -i k" {
		t.Errorf("caller's GIT_SSH_COMMAND replaced: %q", v)
	}
	if _, ok := envValue(netEnv(dir, []string{"GIT_SSH=/opt/wrapper"}), "GIT_SSH_COMMAND"); ok {
		t.Error("GIT_SSH set: GIT_SSH_COMMAND injected over the wrapper")
	}
	runIn(t, dir, "config", "core.sshCommand", "ssh -J jump")
	if _, ok := envValue(netEnv(dir, nil), "GIT_SSH_COMMAND"); ok {
		t.Error("core.sshCommand set: GIT_SSH_COMMAND injected over it")
	}
}
