package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// sshCommand builds a "ssh -i <key> [-o UserKnownHostsFile=... -o
// StrictHostKeyChecking=yes]" string from a key/known-hosts pair, or ""
// if key is empty. Shared by setupGitSSH (global GIT_SSH_COMMAND fallback)
// and gitEnvForKB (per-KB GIT_SSH_COMMAND override).
func sshCommand(key, knownHosts string) string {
	if key == "" {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "ssh -i %s", key)
	if knownHosts != "" {
		fmt.Fprintf(&sb, " -o UserKnownHostsFile=%s -o StrictHostKeyChecking=yes", knownHosts)
	}
	return sb.String()
}

// setupGitSSH configures GIT_SSH_COMMAND from g.SSHKey/g.KnownHosts so that
// git subprocesses (clone, fetch, push) can reach remote KBs over SSH. It is
// a no-op if no SSH key is configured, and never overrides an
// already-set GIT_SSH_COMMAND (the environment wins). This is the global
// fallback for KBs with no per-KB override; see gitEnvForKB for the
// per-KB case, where — inversely — the per-KB env wins over the process
// environment (docs/decisions.md D46).
func setupGitSSH(g config.GitConfig) error {
	cmd := sshCommand(g.SSHKey, g.KnownHosts)
	if cmd == "" {
		return nil
	}
	if _, exists := os.LookupEnv("GIT_SSH_COMMAND"); exists {
		return nil
	}
	return os.Setenv("GIT_SSH_COMMAND", cmd)
}

// resolveKBName resolves the name used for a KB everywhere it matters: the
// HTTP mount endpoint (/mcp/<name>), token scopes (kb:<name>:r|rw), the
// clone destination under Config.Data, and the git-token/SOPS-age-key
// per-KB conventions (GitConfig.TokenDir/<name>.token,
// SopsConfig.AgeKeyDir/<name>.age). spec.Name wins if set; otherwise the
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
// inject a `credential.helper` reading the per-KB token file
// <tokenDir>/<name>.token, if tokenDir is set and that file exists (D53).
// Git only invokes a credential helper for http(s) transports, so this is a
// no-op for ssh:// remotes even though it is unconditionally attempted here.
// The helper is a shell one-liner that `cat`s the token file at
// credential-prompt time: the token itself never appears in argv, the
// remote URL, or .git/config — only the file path is embedded in the env.
func gitTokenCredentialEnv(tokenDir, name string) []string {
	if tokenDir == "" || name == "" {
		return nil
	}
	tokenPath := filepath.Join(tokenDir, name+".token")
	if _, err := os.Stat(tokenPath); err != nil {
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

// gitEnvForKB assembles the per-KB git environment: GIT_SSH_COMMAND (from
// spec.SSHKey/KnownHosts, falling back to g.SSHKey/KnownHosts),
// GIT_COMMITTER_NAME/EMAIL (from spec.CommitterName/Email, falling back to
// spec.AuthorName/Email, then g.CommitterName/Email, then
// g.AuthorName/Email — i.e. the default committer is the default author),
// and — if g.TokenDir/<name>.token exists — a credential.helper (D53,
// gitTokenCredentialEnv). name is the resolved KB name (resolveKBName). Only
// entries that resolve to a non-empty value are included. The result is
// passed to gitx as env: it takes precedence over the process environment
// (runGitEnv), the inverse of setupGitSSH's "process environment wins" rule.
func gitEnvForKB(spec config.KBSpec, g config.GitConfig, name string) []string {
	var env []string

	sshKey, knownHosts := spec.SSHKey, spec.KnownHosts
	if sshKey == "" {
		sshKey = g.SSHKey
	}
	if knownHosts == "" {
		knownHosts = g.KnownHosts
	}
	if cmd := sshCommand(sshKey, knownHosts); cmd != "" {
		env = append(env, "GIT_SSH_COMMAND="+cmd)
	}

	committerName := spec.CommitterName
	if committerName == "" {
		committerName = spec.AuthorName
	}
	if committerName == "" {
		committerName = g.CommitterName
	}
	if committerName == "" {
		committerName = g.AuthorName
	}
	if committerName != "" {
		env = append(env, "GIT_COMMITTER_NAME="+committerName)
	}

	committerEmail := spec.CommitterEmail
	if committerEmail == "" {
		committerEmail = spec.AuthorEmail
	}
	if committerEmail == "" {
		committerEmail = g.CommitterEmail
	}
	if committerEmail == "" {
		committerEmail = g.AuthorEmail
	}
	if committerEmail != "" {
		env = append(env, "GIT_COMMITTER_EMAIL="+committerEmail)
	}

	env = append(env, gitTokenCredentialEnv(g.TokenDir, name)...)

	return env
}

// ensureClonedKB clones remote into <dataDir>/<name> if not already present,
// and returns the destination path. name is the resolved KB name
// (resolveKBName) — it drives the clone destination so an explicit
// KBSpec.Name (D53) also determines where the KB is cloned. If the
// destination already exists and looks like a git repository, it is left
// untouched: the existing git-autocommit/git-sync flow handles fetch/push on
// subsequent writes. env is the per-KB git environment from gitEnvForKB
// (GIT_SSH_COMMAND, credential.helper, etc.).
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
		if err := gitx.Clone(remote, dest, env...); err != nil {
			return "", fmt.Errorf("clone %s: %w", remote, err)
		}
	}
	return dest, nil
}

// remoteKBName derives a KB name from the last path segment of a git remote
// URL, stripping a trailing ".git" suffix. Handles both URL-style
// (ssh://host/path/name.git) and scp-style (git@host:path/name.git) remotes.
func remoteKBName(remote string) string {
	r := strings.TrimSuffix(strings.TrimRight(remote, "/"), ".git")
	if idx := strings.LastIndexAny(r, "/:"); idx >= 0 {
		r = r[idx+1:]
	}
	return r
}

// isGitRepoDir reports whether dest exists and contains a .git entry.
func isGitRepoDir(dest string) bool {
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		return false
	}
	return true
}
