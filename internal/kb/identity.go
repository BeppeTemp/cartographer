package kb

import (
	"os"
	"strings"
	"sync"
)

// Who a commit is by (D904, D950): a write is authored and committed by its
// writer (mcpserver.gitWrap); every other commit Cartographer makes (sync
// merges, the external-changes commit, conflict resolution, a rebase's
// replayed commits), and a write that names no writer, by the KB's default
// identity — the one the operator configured for it (git.identity and
// git.identities in the server file, SetDefaultIdentity), else the server
// identity. TRAP: a forge with a membership push rule (GitLab member_check)
// checks the committer as well as the author, so the committer is never the
// server while a writer or a configured identity is known; which instance
// recorded a write is the Cartographer-Host trailer.
const serverIdentityName = "Cartographer"

// AuthorSettingRemoved is the reason given wherever a removed author or
// committer setting is met again (D904), and what takes its place.
const AuthorSettingRemoved = "the author and the committer of a write are the writer's own git identity, sent by the client; a commit with no writer is by git.identity (or git.identities) of the server file, else by the server identity (D950)"

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

// ServerHost is the machine part of the server identity: what the
// Cartographer-Host trailer of a write records (D950).
func ServerHost() string {
	_, email := ServerIdentity()
	return strings.TrimPrefix(email, "cartographer@")
}

// IdentityGitEnv is name/email as both the author and the committer git
// variables: the identity of any commit git creates under it, and the author
// of one that names no author (a merge, a stash).
func IdentityGitEnv(name, email string) []string {
	return []string{
		"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email,
		"GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + email,
	}
}

// ServerGitEnv is the server identity as git environment variables
// (IdentityGitEnv): the identity of a commit with no writer known.
func ServerGitEnv() []string {
	return IdentityGitEnv(ServerIdentity())
}

// SetDefaultIdentity sets the identity this KB's commits with no writer of
// their own are by (D950), from the server file; both empty removes it. The
// server calls it once, when it mounts the KB, before any write.
func (k *KB) SetDefaultIdentity(name, email string) {
	k.defaultName, k.defaultEmail = name, email
}

// ConfiguredIdentity is the identity the operator configured for this KB, ok
// false when there is none.
func (k *KB) ConfiguredIdentity() (name, email string, ok bool) {
	if k == nil || k.defaultName == "" || k.defaultEmail == "" {
		return "", "", false
	}
	return k.defaultName, k.defaultEmail, true
}

// DefaultIdentity is who a commit with no writer of its own is by (D950): the
// identity configured for this KB, else the server identity.
func (k *KB) DefaultIdentity() (name, email string) {
	if n, e, ok := k.ConfiguredIdentity(); ok {
		return n, e
	}
	return ServerIdentity()
}

// GitCommandEnv is the environment of every git command run for this KB: the
// per-KB GitEnv (SSH, credentials) and, last so that nothing in the process
// environment or in git's own config can displace it, the KB's default
// identity (DefaultIdentity).
func (k *KB) GitCommandEnv() []string {
	return append(append([]string{}, k.GitEnv...), IdentityGitEnv(k.DefaultIdentity())...)
}

// gitCommandEnvAs is GitCommandEnv for a commit by name/email: its author and
// its committer (D950). Either empty means the default identity.
func (k *KB) gitCommandEnvAs(name, email string) []string {
	if name == "" || email == "" {
		return k.GitCommandEnv()
	}
	return append(append([]string{}, k.GitEnv...), IdentityGitEnv(name, email)...)
}
