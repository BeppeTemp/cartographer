package gitx

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MaxIdentBytes bounds an identity: a name and an email, not a document.
const MaxIdentBytes = 256

// ParseIdent validates the "Name <email>" identity a client sends as the
// author of a write (D904); problem says why a value is refused. The server
// applies it to the Cartographer-Author header and the client to what it is
// about to send, so a value one refuses the other never sends. TRAP: the value
// ends up in a commit's author line; control characters (newlines first) and
// angle brackets inside either part are refused, not cleaned.
func ParseIdent(raw string) (name, email, problem string) {
	s := strings.TrimSpace(raw)
	switch {
	case s == "":
		return "", "", "empty"
	case len(s) > MaxIdentBytes:
		return "", "", "longer than 256 bytes"
	case !utf8.ValidString(s):
		return "", "", "not valid UTF-8"
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return "", "", "contains a control character"
		}
	}
	open := strings.LastIndex(s, "<")
	if open < 0 || !strings.HasSuffix(s, ">") {
		return "", "", "not in the form Name <email>"
	}
	name = strings.TrimSpace(s[:open])
	email = s[open+1 : len(s)-1]
	if !hasLetterOrDigit(name) || strings.ContainsAny(name, "<>") {
		return "", "", "the name is missing or holds angle brackets"
	}
	at := strings.Index(email, "@")
	if at <= 0 || at == len(email)-1 || strings.Count(email, "@") != 1 ||
		strings.ContainsAny(email, "<> \t\"") {
		return "", "", "the email is not a plain local@domain"
	}
	return name, email, ""
}

// workTreeIdentTimeout bounds the git commands WorkTreeIdent runs: it is asked
// on the path of a write, and git reading its config takes milliseconds.
const workTreeIdentTimeout = 2 * time.Second

// WorkTreeIdent returns the author git would record for a commit made in dir,
// as "Name <email>", or "" when there is none to send (D904). Only inside a
// work tree: outside one git applies no includeIf gitdir:, and would answer
// with the global identity a per-repository setup exists to keep away from a
// work KB. user.useConfigOnly stops git from making up user@host from the
// machine; GIT_AUTHOR_NAME and GIT_AUTHOR_EMAIL still apply, as for a commit.
// GIT_DIR and GIT_WORK_TREE are dropped so dir alone decides the repository.
// An identity ParseIdent refuses is not sent either.
func WorkTreeIdent(dir string) string {
	if dir == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), workTreeIdentTimeout)
	defer cancel()
	env := []string{"GIT_TERMINAL_PROMPT=0"}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_DIR=") && !strings.HasPrefix(kv, "GIT_WORK_TREE=") {
			env = append(env, kv)
		}
	}
	git := func(args ...string) (string, bool) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "-c", "user.useConfigOnly=true"}, args...)...)
		cmd.Env = env
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err == nil
	}
	if out, ok := git("rev-parse", "--is-inside-work-tree"); !ok || out != "true" {
		return ""
	}
	out, ok := git("var", "GIT_AUTHOR_IDENT")
	end := strings.LastIndex(out, ">")
	if !ok || end < 0 {
		return ""
	}
	name, email, problem := ParseIdent(out[:end+1])
	if problem != "" {
		return ""
	}
	return name + " <" + email + ">"
}

func hasLetterOrDigit(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
