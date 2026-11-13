package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/procattr"
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

// globalIdentCallTimeout bounds each git command GlobalIdent runs, not the
// whole function: the includeIf files add one command each, and one budget for
// all of them would expire on a slow host and turn a harmless setup into the
// per-folder warning. A variable so a test can shrink it.
var globalIdentCallTimeout = workTreeIdentTimeout

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
	git := func(args ...string) (string, bool) {
		cmd := procattr.Hide(exec.CommandContext(ctx, "git", append([]string{"-C", dir, "-c", "user.useConfigOnly=true"}, args...)...))
		cmd.Env = identEnv()
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

// GlobalIdent returns the identity the global git config names, as
// "Name <email>", for a client entry every folder shares (D904). It is "" with
// perFolder true when the config includes files by condition (includeIf) and
// one of them may set an identity: a folder could then get another one, which
// a value written once into an entry cannot follow, and a KB must not receive
// the identity a per-folder setup keeps away from it. An includeIf whose files
// set no user, author or committer name or email (a per-organisation
// core.sshCommand, say) changes no identity and counts for nothing (#1910).
// Whatever cannot be shown to be harmless counts as an identity: an unreadable,
// missing or non-regular file, a nested includeIf, a key that is not a path. It is "" alone
// when the config names no valid identity or cannot be read.
// GIT_AUTHOR_NAME and GIT_AUTHOR_EMAIL are not read: they belong to one
// process, and the value goes into a file every later session reads.
func GlobalIdent() (ident string, perFolder bool) {
	git := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), globalIdentCallTimeout)
		defer cancel()
		cmd := procattr.Hide(exec.CommandContext(ctx, "git", append([]string{"config"}, args...)...))
		cmd.Env = identEnv()
		cmd.Dir = os.TempDir()
		return cmd.Output()
	}
	// git reads the XDG file first and ~/.gitconfig after it, which wins; its
	// own --global reads only one of them. So each file is read on its own and
	// the values merged here with the same precedence.
	scopes := globalScopes()
	for _, scope := range scopes {
		keys, err := git(append(append([]string{}, scope...), "--includes", "--null", "--get-regexp", `^includeif\.`)...)
		if err == nil {
			if includeIfMaySetIdentity(string(keys), git, scope) {
				return "", true
			}
		} else if exitCode(err) != 1 {
			return "", false
		}
	}
	global := func(key string) (value string, found bool) {
		for _, scope := range scopes {
			out, err := git(append(append([]string{}, scope...), "--includes", "--get", key)...)
			if err == nil {
				value, found = strings.TrimSpace(string(out)), true
			}
		}
		return value, found
	}
	name, okName := global("user.name")
	email, okEmail := global("user.email")
	if !okName || !okEmail {
		return "", false
	}
	n, e, problem := ParseIdent(name + " <" + email + ">")
	if problem != "" {
		return "", false
	}
	return n + " <" + e + ">", false
}

// includeIfMaySetIdentity says whether any file a conditional include of the
// global config names can set an identity. keys is the NUL-separated
// `key\nvalue` listing of every includeif.* key; git runs `git config`.
// Conditions are not evaluated: outside a repository gitdir: and onbranch:
// never match, but the file may be included in the folder a client entry is
// used from, so every conditional include counts.
// scope selects the file the listing came from (globalScopes).
func includeIfMaySetIdentity(keys string, git func(args ...string) ([]byte, error), scope []string) bool {
	for _, rec := range strings.Split(keys, "\x00") {
		key, _, _ := strings.Cut(rec, "\n")
		if rec != "" && !strings.HasSuffix(key, ".path") {
			return true // an includeif key this code does not know how to follow
		}
	}
	// --type=path lets git expand ~ with its own HOME; --null because a
	// condition may hold spaces. Records are origin NUL key LF value NUL.
	out, err := git(append(append([]string{}, scope...), "--includes", "--show-origin", "--null", "--type=path",
		"--get-regexp", `^includeif\..*\.path$`)...)
	if err != nil {
		return true
	}
	fields := strings.Split(string(out), "\x00")
	if len(fields) < 2 || fields[len(fields)-1] != "" || len(fields)%2 != 1 {
		return true
	}
	for i := 0; i+1 < len(fields); i += 2 {
		origin, rec := fields[i], fields[i+1]
		file, isFile := strings.CutPrefix(origin, "file:")
		_, value, hasValue := strings.Cut(rec, "\n")
		if !isFile || !hasValue || value == "" {
			return true
		}
		p, ok := resolveIncludePath(file, value)
		if !ok || !readableFile(p) {
			return true
		}
		// A nested includeif is not evaluated outside a repository, so its
		// files cannot be read here: it counts as an identity.
		_, err := git("--file", p, "--includes", "--null", "--get-regexp",
			`^(user|author|committer)\.(name|email)$|^includeif\.`)
		if err == nil || exitCode(err) != 1 {
			return true
		}
	}
	return false
}

// globalScopes are the git config selectors of the files `git config --global`
// stands for, in git's reading order (the later overrides the earlier): the XDG
// file, then ~/.gitconfig. git's --global alone reads only ~/.gitconfig when it
// exists and never the XDG file then, though a commit reads both. A file that
// is missing is left out. GIT_CONFIG_GLOBAL replaces both files for git, so it
// stays one --global selector, and set but empty it means no global file at
// all (git tests getenv for non-NULL, not for a non-empty value).
func globalScopes() [][]string {
	if v, set := os.LookupEnv("GIT_CONFIG_GLOBAL"); set {
		if v == "" {
			return nil
		}
		return [][]string{{"--global"}}
	}
	home := os.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" && home != "" {
		xdg = filepath.Join(home, ".config")
	}
	var files []string
	if xdg != "" {
		files = append(files, filepath.Join(xdg, "git", "config"))
	}
	if home != "" {
		files = append(files, filepath.Join(home, ".gitconfig"))
	}
	var scopes [][]string
	for _, f := range files {
		if _, err := os.Stat(f); err == nil {
			scopes = append(scopes, []string{"--file", f})
		}
	}
	return scopes
}

// resolveIncludePath is the file an include path names, as git resolves it:
// a relative path is relative to the file that includes it. ok is false when
// the path cannot be resolved with certainty, which counts as an identity.
// Git for Windows prints forward slashes; an MSYS-style /c/... path names no
// file here. TRAP: on Windows a rooted path with no drive (`/etc/x`, `\x`) is
// not absolute, and joining it to the including file's directory would read
// another file than the one git reads (or the MSYS one) and call it harmless.
func resolveIncludePath(file, value string) (p string, ok bool) {
	p = filepath.FromSlash(value)
	if filepath.IsAbs(p) {
		return p, true
	}
	if p != "" && os.IsPathSeparator(p[0]) {
		return "", false
	}
	return filepath.Join(filepath.Dir(filepath.FromSlash(file)), p), true
}

// readableFile says whether p is a regular file this process can open. TRAP:
// os.Stat is not enough. git exits 1 for a file it cannot read (mode 000, a
// directory) exactly as for a file with no match, so "git found nothing" would
// be read as "the file sets no identity" for a file git never read. TRAP: the
// open must not block, and a Stat before it does not make it safe: a path
// swapped for a FIFO between the Stat and the open would wait for a writer
// forever, outside any timeout, and hang sync and connect. So the file is
// opened without blocking (openNoBlock) and the descriptor itself is checked
// regular; there is no earlier Stat to race. Every failure is false.
func readableFile(p string) bool {
	f, err := openNoBlock(p)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	return err == nil && fi.Mode().IsRegular()
}

// exitCode is the exit status of a failed command, or -1 when it did not run
// to an exit (not found, timed out, killed).
func exitCode(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

// identEnv is the environment of the git commands that read an identity:
// the variables that pin a repository are dropped so no repository is forced
// on them, and no prompt can block a write.
func identEnv() []string {
	return append([]string{"GIT_TERMINAL_PROMPT=0"}, withoutRepoPins(os.Environ())...)
}

func hasLetterOrDigit(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
