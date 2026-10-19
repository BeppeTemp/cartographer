package kb

import (
	"os"
	"strings"
	"sync"
)

// The server identity (D904) is the committer of every commit Cartographer
// makes, and the author of every commit that is not a write: sync merges, the
// external-changes commit, conflict resolution, KB init. The author of a write
// is whoever wrote it (mcpserver.gitWrap); it is never configured here.
const serverIdentityName = "Cartographer"

var (
	serverIdentityOnce  sync.Once
	serverIdentityEmail string
)

// ServerIdentity returns the technical identity Cartographer commits as:
// "Cartographer" and "cartographer@<host>", <host> being the machine name, so a
// commit made by a pod or a workstation says which one made it. It carries no
// key and no account on any forge.
func ServerIdentity() (name, email string) {
	serverIdentityOnce.Do(func() {
		host, _ := os.Hostname()
		serverIdentityEmail = "cartographer@" + identityHost(host)
	})
	return serverIdentityName, serverIdentityEmail
}

// identityHost reduces a machine name to what an email domain accepts, so an
// unusual hostname cannot make the identity something git refuses.
func identityHost(host string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(host)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if h := strings.Trim(b.String(), ".-"); h != "" {
		return h
	}
	return "localhost"
}

// ServerGitEnv is the server identity as git environment variables: the
// committer of any commit git creates, and the author of one that names no
// author (a merge, a stash). A commit made with --author keeps its author.
func ServerGitEnv() []string {
	name, email := ServerIdentity()
	return []string{
		"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email,
		"GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + email,
	}
}

// GitCommandEnv is the environment of every git command run for this KB: the
// per-KB GitEnv (SSH, credentials) and, last so that nothing in the process
// environment or in git's own config can displace it, the server identity.
func (k *KB) GitCommandEnv() []string {
	return append(append([]string{}, k.GitEnv...), ServerGitEnv()...)
}
