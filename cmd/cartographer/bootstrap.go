package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// sshCommand builds a "ssh -i <key> [-o UserKnownHostsFile=... -o
// StrictHostKeyChecking=yes]" string from a key/known-hosts pair, or ""
// if key is empty. Shared by setupGitSSH (global GIT_SSH_COMMAND fallback)
// and gitEnvForKB (per-KB GIT_SSH_COMMAND override). git runs it through the
// shell, so each path is single-quoted: a space would split it and the
// backslashes of a Windows credentials directory would be eaten. ssh splits
// the UserKnownHostsFile value on whitespace itself, hence the double quotes.
func sshCommand(key, knownHosts string) string {
	if key == "" {
		return ""
	}
	cmd := "ssh -i " + shellQuote(key)
	if knownHosts != "" {
		cmd += " -o " + shellQuote(`UserKnownHostsFile="`+knownHosts+`"`) + " -o StrictHostKeyChecking=yes"
	}
	return cmd
}

// shellQuote quotes s as one word for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// setupGitSSH configures GIT_SSH_COMMAND from the default SSH key of the
// credentials directory (default.ssh, with its known_hosts) so that git
// subprocesses (clone, fetch, push) can reach remote KBs over SSH. It is a
// no-op if there is no such key, and never overrides an already-set
// GIT_SSH_COMMAND (the environment wins). This is the global fallback for KBs
// with no key of their own; for the per-KB case, where the more specific
// setting wins over the process environment (D46), see gitEnvForKB.
func setupGitSSH(credDir string) error {
	if _, exists := os.LookupEnv("GIT_SSH_COMMAND"); exists {
		return nil
	}
	key, ok := credentialFile(credDir, "", config.CredentialSSH)
	if !ok {
		return nil
	}
	return os.Setenv("GIT_SSH_COMMAND", sshCommandWithHosts(credDir, key))
}

// sshCommandWithHosts is sshCommand with the credentials directory's one
// known_hosts, when it has one. It holds public host keys, 0644 like OpenSSH's
// own, so its permissions are not warned about.
func sshCommandWithHosts(credDir, key string) string {
	hosts, _ := config.SharedCredentialFile(credDir, config.KnownHostsFile)
	return sshCommand(key, hosts)
}

// resolveKBName resolves the name used for a KB everywhere it matters: the
// HTTP mount endpoint (/mcp/<name>), token scopes (kb:<name>:r|rw), the
// clone destination under Config.Data, and the names of its files in the
// credentials directory (<name>.ssh, <name>.token, <name>.age, <name>.signing;
// D905). spec.Name wins if set; otherwise the
// name is derived exactly as before D53: the last path segment of the
// remote (remoteKBName) for remote KBs, or the basename of path for local
// ones (path is spec.Path for explicit KBs, "" for remotes — resolved after
// cloning is not needed since the name drives the clone destination itself).
func resolveKBName(spec config.KBSpec, path string) string {
	if spec.Name != "" {
		return spec.Name
	}
	if spec.Remote != "" {
		return remoteKBName(spec.Remote)
	}
	return filepath.Base(strings.TrimRight(path, string(os.PathSeparator)))
}

// gitTokenCredentialEnv returns the GIT_CONFIG_* environment entries that
// inject a `credential.helper` reading the token file at tokenPath, the
// <kb>.token or default.token of the credentials directory (D905); no path, no
// helper. Git only invokes a credential helper for http(s) transports, so this
// is a no-op for ssh:// remotes even though it is attempted for every KB.
// The helper is a shell one-liner that `cat`s the token file at
// credential-prompt time: the token itself never appears in argv, the
// remote URL, or .git/config — only the file path is embedded in the env.
func gitTokenCredentialEnv(tokenPath string) []string {
	if tokenPath == "" {
		return nil
	}
	escaped := strings.ReplaceAll(tokenPath, "'", `'\''`)
	helper := fmt.Sprintf(`!f() { echo username=token; echo "password=$(cat '%s')"; }; f`, escaped)
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=credential.helper",
		"GIT_CONFIG_VALUE_0=" + helper,
	}
}

// gitEnvForKB assembles the per-KB git environment: GIT_SSH_COMMAND from the
// KB's own key in the credentials directory, <name>.ssh (a KB without one uses
// default.ssh through the process environment, setupGitSSH, and so an explicit
// GIT_SSH_COMMAND still wins over the default), and — if <name>.token or
// default.token exists — a credential.helper (D905,
// gitTokenCredentialEnv). name is the resolved KB name (resolveKBName). Only
// entries that resolve to a non-empty value are included. It carries no
// identity: the KB's default identity is added by kb.KB.GitCommandEnv (D950). The result is
// passed to gitx as env: it takes precedence over the process environment
// (runGitEnv).
func gitEnvForKB(name, credDir string) []string {
	var env []string

	if key, ok := ownCredentialFile(credDir, name, config.CredentialSSH); ok {
		env = append(env, "GIT_SSH_COMMAND="+sshCommandWithHosts(credDir, key))
	}

	if tokenPath, ok := credentialFile(credDir, name, config.CredentialToken); ok {
		env = append(env, gitTokenCredentialEnv(tokenPath)...)
	}

	return env
}

// ensureClonedKB clones remote into <dataDir>/<name> if not already present,
// and returns the destination path. name is the resolved KB name
// (resolveKBName) — it drives the clone destination so an explicit
// KBSpec.Name (D53) also determines where the KB is cloned. If the
// destination already exists and looks like a git repository, it is left
// untouched: the sync that follows the remote handles fetch/push on subsequent
// writes. env is the per-KB git environment from gitEnvForKB
// (GIT_SSH_COMMAND, credential.helper, etc.).
//
// A fresh clone is checked out on the `branch` its settings.yaml sets (D335,
// D903) — tracking it when the remote has it, created from the remote's default
// branch otherwise — read from the default branch the clone has just checked
// out. The clone of an empty remote is left unborn: serve cannot mount it and says to create the KB with `kb create` (D906). An existing
// clone is never switched: SyncIn refuses its writes if it sits on another
// branch.
func ensureClonedKB(remote, name, dataDir string, env ...string) (string, error) {
	if dataDir == "" {
		return "", fmt.Errorf("remote KB %q requires a data directory to clone into (--data, CARTOGRAPHER_DATA, or data: in the YAML config)", remote)
	}
	if name == "" {
		return "", fmt.Errorf("cannot derive KB name from remote %q", remote)
	}
	dest := filepath.Join(dataDir, name)

	if !isGitRepoDir(dest) {
		log.Printf("cloning KB %q from %s to %s", name, remote, dest)
		// Clone beside the destination and rename when done (D348): git
		// creates .git first, so a process killed mid-clone (an emptyDir
		// survives a container restart) would leave a dest that the next
		// start takes for a finished clone. The dot prefix keeps the
		// temporary directory out of KB discovery.
		tmp := filepath.Join(dataDir, "."+name+".cloning")
		if err := os.RemoveAll(tmp); err != nil {
			return "", fmt.Errorf("clone %s: remove stale %s: %w", remote, tmp, err)
		}
		// No deadline here: a server bootstrapping a large KB must not have
		// its clone killed mid-way. Clone's non-interactive environment is
		// what keeps this from hanging on a prompt (D173).
		if err := gitx.Clone(context.Background(), remote, tmp, env...); err != nil {
			_ = os.RemoveAll(tmp)
			return "", fmt.Errorf("clone %s: %w", remote, err)
		}
		if err := checkoutConfiguredBranch(tmp, committedBranchSetting(tmp), env...); err != nil {
			_ = os.RemoveAll(tmp)
			return "", fmt.Errorf("clone %s: %w", remote, err)
		}
		// dest may exist without .git (an empty directory): clear it so the
		// rename is portable.
		if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
			_ = os.RemoveAll(tmp)
			return "", fmt.Errorf("clone %s: %w", remote, err)
		}
		if err := os.Rename(tmp, dest); err != nil {
			_ = os.RemoveAll(tmp)
			return "", fmt.Errorf("clone %s: %w", remote, err)
		}
	}
	return dest, nil
}

// committedBranchSetting is the `branch` of the settings.yaml committed at HEAD
// of a fresh clone, which is the remote's default branch; "" when there is none.
func committedBranchSetting(dest string) string {
	text, err := gitx.ShowFile(dest, "HEAD", kb.SettingsFile)
	if err != nil {
		return ""
	}
	s, _, perr := kb.ParseSettings([]byte(text))
	if perr != nil {
		return ""
	}
	v, _ := s.String("branch")
	return v
}

// checkoutConfiguredBranch puts a fresh clone on the configured branch (D335).
func checkoutConfiguredBranch(dest, branch string, env ...string) error {
	if branch == "" || gitx.HeadUnborn(dest) {
		return nil
	}
	if current, _ := gitx.Branch(dest); current == branch {
		return nil
	}
	if gitx.RemoteBranchExists(dest, "origin", branch) {
		return gitx.CheckoutNewBranch(dest, branch, "origin/"+branch, env...)
	}
	return gitx.CheckoutNewBranch(dest, branch, "HEAD", env...)
}

// remoteKBName derives a KB name from the last path segment of a git remote
// URL, stripping a trailing ".git" suffix. Handles both URL-style
// (ssh://host/path/name.git) and scp-style (git@host:path/name.git) remotes.
func remoteKBName(remote string) string { return config.RemoteKBName(remote) }

// isGitRepoDir reports whether dest exists and contains a .git entry.
func isGitRepoDir(dest string) bool {
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		return false
	}
	return true
}
