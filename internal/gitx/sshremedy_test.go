package gitx

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSSHRemedyCases(t *testing.T) {
	savedA, savedK, savedO, savedC := sshAgentIdentities, sshIdentityFiles, sshGOOS, sshCustomCommand
	t.Cleanup(func() {
		sshAgentIdentities, sshIdentityFiles, sshGOOS, sshCustomCommand = savedA, savedK, savedO, savedC
	})
	denied := "git@gitlab.example.com: Permission denied (publickey)."
	cases := []struct {
		name, remote, out, goos string
		agent                   int
		keys                    []string
		want                    []string
	}{
		{"no key", "git@gitlab.example.com:g/r.git", denied, "linux", 2, nil,
			[]string{"no SSH key", "ssh-keygen -t ed25519", "https://gitlab.example.com/-/user_settings/ssh_keys"}},
		{"no key agent empty github", "git@github.com:o/r.git", denied, "linux", 1, nil,
			[]string{"no SSH key", "https://github.com/settings/keys"}},
		{"key not on forge", "ssh://git@gitea.example.com/o/r.git", denied, "linux", 0, []string{"/h/.ssh/id_ed25519"},
			[]string{"does not know the SSH key", "https://gitea.example.com/user/settings/keys"}},
		{"unknown forge", "git@example.com:o/r.git", denied, "linux", 0, []string{"/h/.ssh/id_rsa"},
			[]string{"to your account on example.com"}},
		{"passphrase macos", "git@github.com:o/r.git", denied, "darwin", 1, []string{"/h/.ssh/id_ed25519"},
			[]string{"ssh-add --apple-use-keychain /h/.ssh/id_ed25519", "Either the forge does not know it", "passphrase"}},
		{"passphrase linux", "git@codeberg.org:o/r.git", "Enter passphrase for key", "linux", 2, []string{"/h/.ssh/id_ed25519"},
			[]string{`eval "$(ssh-agent -s)" && ssh-add /h/.ssh/id_ed25519`, "https://codeberg.org/user/settings/keys"}},
		{"passphrase windows", "git@github.com:o/r.git", denied, "windows", 2, []string{"/h/.ssh/id_ed25519"},
			[]string{"Start-Service ssh-agent", "core.sshCommand C:/Windows/System32/OpenSSH/ssh.exe"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sshAgentIdentities = func(context.Context) int { return c.agent }
			sshIdentityFiles = func(context.Context, string) []string { return c.keys }
			sshCustomCommand = func(context.Context) bool { return false }
			sshGOOS = c.goos
			got := sshRemedy(context.Background(), c.remote, c.out)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("remedy %q lacks %q", got, w)
				}
			}
		})
	}
}

func TestSSHRemedyIgnoresOthers(t *testing.T) {
	if got := sshRemedy(context.Background(), "https://example.com/r.git", "Permission denied (publickey)"); got != "" {
		t.Errorf("https remote classified: %q", got)
	}
	if got := sshRemedy(context.Background(), "git@example.com:r.git", "Could not resolve host"); got != "" {
		t.Errorf("non-auth failure classified: %q", got)
	}
}

func TestSSHRemedyCustomCommandIsHedged(t *testing.T) {
	savedA, savedK, savedC := sshAgentIdentities, sshIdentityFiles, sshCustomCommand
	t.Cleanup(func() { sshAgentIdentities, sshIdentityFiles, sshCustomCommand = savedA, savedK, savedC })
	sshAgentIdentities = func(context.Context) int { return 2 }
	sshIdentityFiles = func(context.Context, string) []string { return nil }
	sshCustomCommand = func(context.Context) bool { return true }
	got := sshRemedy(context.Background(), "git@github.com:o/r.git", "Permission denied (publickey).")
	if strings.Contains(got, "has no SSH key") || !strings.Contains(got, "custom ssh command") || !strings.Contains(got, "ssh-keygen") {
		t.Errorf("custom ssh command not hedged: %q", got)
	}
}

func TestParseIdentityFiles(t *testing.T) {
	dump := "host x\nidentityfile ~/.ssh/work_key\nidentityfile %d/.ssh/alt\nidentityfile ~/.ssh/%h_key\nport 22\n"
	got := parseIdentityFiles(dump, "/h")
	if len(got) != 2 || got[0] != "/h/.ssh/work_key" || got[1] != "/h/.ssh/alt" {
		t.Errorf("parseIdentityFiles = %v", got)
	}
}

func TestSSHHost(t *testing.T) {
	for remote, want := range map[string]string{
		"ssh://user@example.com:2222/o/r.git": "example.com",
		"user@example.com:o/r.git":            "example.com",
		"git@[::1]:o/r.git":                   "::1",
		"https://example.com/o/r.git":         "",
	} {
		if got := sshHost(remote); got != want {
			t.Errorf("sshHost(%q) = %q, want %q", remote, got, want)
		}
	}
}

// The preflight's ls-remote failure carries the classified remedy, not the
// generic row.
func TestLsRemoteProbeClassifiesSSHFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	stub := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho 'git@gitlab.example.com: Permission denied (publickey).' >&2\nexit 255\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSH_COMMAND", stub)
	savedA, savedK, savedC := sshAgentIdentities, sshIdentityFiles, sshCustomCommand
	t.Cleanup(func() { sshAgentIdentities, sshIdentityFiles, sshCustomCommand = savedA, savedK, savedC })
	sshAgentIdentities = func(context.Context) int { return 2 }
	sshIdentityFiles = func(context.Context, string) []string { return nil }
	sshCustomCommand = func(context.Context) bool { return false }
	_, err := lsRemoteProbe(context.Background(), "git@gitlab.example.com:g/r.git")
	if err == nil || !strings.Contains(err.Error(), "no SSH key") || strings.Contains(err.Error(), "check the key loaded in your agent") {
		t.Fatalf("err = %v", err)
	}
}
