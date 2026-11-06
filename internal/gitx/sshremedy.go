package gitx

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/procattr"
)

// Probes behind sshRemedy, variables so tests inject the machine. Setup only
// diagnoses and prints commands: it never creates a key nor uploads one (the
// same principle as host keys, D173).
var (
	// sshAgentIdentities runs "ssh-add -l" and returns its exit status:
	// 0 identities loaded, 1 agent without identities, 2 no agent reachable
	// (or ssh-add missing, reported as 2).
	sshAgentIdentities = func(ctx context.Context) int {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		err := procattr.Hide(exec.CommandContext(ctx, "ssh-add", "-l")).Run()
		var ee *exec.ExitError
		switch {
		case err == nil:
			return 0
		case errors.As(err, &ee) && ee.ExitCode() == 1:
			return 1
		default:
			return 2
		}
	}
	// sshIdentityFiles returns the private key files that exist for host:
	// the identityfile lines "ssh -G host" resolves (ssh_config included),
	// plus the default key names.
	sshIdentityFiles = func(ctx context.Context, host string) []string {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		cands := parseIdentityFiles(sshConfigDump(ctx, host), home)
		for _, n := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
			cands = append(cands, filepath.Join(home, ".ssh", n))
		}
		seen := map[string]bool{}
		var found []string
		for _, p := range cands {
			if seen[p] {
				continue
			}
			seen[p] = true
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				found = append(found, p)
			}
		}
		return found
	}
	// sshCustomCommand reports whether git is told to run its own ssh
	// (GIT_SSH_COMMAND, GIT_SSH or core.sshCommand): then the keys ssh would
	// pick are not ours to guess.
	sshCustomCommand = func(ctx context.Context) bool {
		if os.Getenv("GIT_SSH_COMMAND") != "" || os.Getenv("GIT_SSH") != "" {
			return true
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		out, err := procattr.Hide(exec.CommandContext(ctx, "git", "config", "--get", "core.sshCommand")).Output()
		return err == nil && strings.TrimSpace(string(out)) != ""
	}
	sshGOOS = runtime.GOOS
)

// sshConfigDump runs "ssh -G host" (fixed arguments, bounded) and returns its
// output, "" on any failure.
func sshConfigDump(ctx context.Context, host string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := procattr.Hide(exec.CommandContext(ctx, "ssh", "-G", "--", host)).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// parseIdentityFiles takes the identityfile lines of "ssh -G" output, with ~
// and %d expanded; entries with any other token are skipped.
func parseIdentityFiles(dump, home string) []string {
	var files []string
	for _, line := range strings.Split(dump, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || !strings.EqualFold(k, "identityfile") {
			continue
		}
		v = strings.TrimSpace(v)
		if v == "~" || strings.HasPrefix(v, "~/") {
			v = home + v[1:]
		}
		v = strings.ReplaceAll(v, "%d", home)
		if strings.Contains(v, "%") {
			continue
		}
		files = append(files, v)
	}
	return files
}

// sshHost returns the host of an ssh remote (URL or scp-like), "" otherwise.
func sshHost(remote string) string {
	// scp-like with a bracketed IPv6 literal (git@[::1]:path), which
	// isSSHRemote's first-colon split does not see.
	if !strings.Contains(remote, "://") {
		if i, j := strings.Index(remote, "["), strings.Index(remote, "]:"); i >= 0 && j > i && !strings.Contains(remote[:i], "/") {
			return remote[i+1 : j]
		}
	}
	if !isSSHRemote(remote) {
		return ""
	}
	if strings.HasPrefix(remote, "ssh://") {
		u, err := url.Parse(remote)
		if err != nil {
			return ""
		}
		return u.Hostname()
	}
	host, _, _ := strings.Cut(remote, ":")
	if _, h, ok := strings.Cut(host, "@"); ok {
		host = h
	}
	return strings.Trim(host, "[]")
}

// sshKeyPage is the line telling where to add a public key, by host heuristic.
func sshKeyPage(host string) string {
	h := strings.ToLower(host)
	switch {
	case h == "github.com":
		return "add the public key at https://github.com/settings/keys"
	case strings.Contains(h, "gitlab"):
		return fmt.Sprintf("add the public key at https://%s/-/user_settings/ssh_keys", host)
	case strings.Contains(h, "gitea") || h == "codeberg.org":
		return fmt.Sprintf("add the public key at https://%s/user/settings/keys", host)
	}
	return fmt.Sprintf("add the public key to your account on %s", host)
}

// sshPassphraseHelp explains how to give git a passphrase-protected key.
func sshPassphraseHelp(goos, key string) string {
	switch goos {
	case "darwin":
		return fmt.Sprintf("load it once into the agent: ssh-add --apple-use-keychain %s", key)
	case "windows":
		return "start the OpenSSH agent service and load the key: " +
			"Get-Service ssh-agent | Set-Service -StartupType Automatic; Start-Service ssh-agent; ssh-add " + key +
			"; then make Git for Windows use that agent: git config --global core.sshCommand C:/Windows/System32/OpenSSH/ssh.exe"
	}
	return fmt.Sprintf("load it once into an agent: eval \"$(ssh-agent -s)\" && ssh-add %s", key)
}

// sshRemedy classifies an SSH authentication failure of remote from git's
// output and this machine's agent and key files, or returns "" when output is
// not one (the generic remoteRemedy row then applies). Three cases: no key at
// all, a key the forge does not know, a passphrase-protected key no agent
// holds.
func sshRemedy(ctx context.Context, remote, output string) string {
	host := sshHost(remote)
	if host == "" {
		return ""
	}
	lower := strings.ToLower(output)
	denied := strings.Contains(lower, "permission denied (publickey")
	pass := strings.Contains(lower, "enter passphrase") || strings.Contains(lower, "no tty present")
	if !denied && !pass {
		return ""
	}
	keys := sshIdentityFiles(ctx, host)
	agent := sshAgentIdentities(ctx)
	page := sshKeyPage(host)
	key := "$HOME/.ssh/id_ed25519"
	if len(keys) > 0 {
		key = keys[0]
	}
	create := "create one with ssh-keygen -t ed25519 -C \"user@example.com\", print it with cat $HOME/.ssh/id_ed25519.pub, and " + page
	switch {
	case sshCustomCommand(ctx):
		return "git runs a custom ssh command (GIT_SSH_COMMAND, GIT_SSH or core.sshCommand), so the key it offers is not known here. " +
			"Either it has no key: " + create + "; or the forge does not know its key: " + page +
			"; or the key has a passphrase and no agent holds it: " + sshPassphraseHelp(sshGOOS, key)
	case !pass && len(keys) == 0 && agent != 0:
		return "this machine has no SSH key (none in the ssh configuration or under $HOME/.ssh, and no agent identity): " + create
	case pass:
		return fmt.Sprintf("the SSH key %s needs a passphrase and no agent holds it: %s. If it is already loaded, %s",
			key, sshPassphraseHelp(sshGOOS, key), page)
	case len(keys) > 0 && agent != 0:
		return fmt.Sprintf("the SSH key %s was refused, and no agent holds it. Either the forge does not know it: %s; "+
			"or it has a passphrase and git cannot ask for it: %s", key, page, sshPassphraseHelp(sshGOOS, key))
	}
	return "the forge does not know the SSH key this machine offers: " + page
}
