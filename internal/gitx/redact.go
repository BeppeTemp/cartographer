package gitx

import (
	"bytes"
	"context"
	"io"
	neturl "net/url"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/procattr"
)

// RedactRemoteURL strips credential material from a remote URL. For http(s)
// (and any "+http"/"+https" transport such as git+https) the whole userinfo is
// dropped: a token can sit in either half (https://token@host/... as well as
// https://user:token@host/...). For other schemes only the password is dropped
// — an ssh username is not a secret and identifies the remote. The query
// string and the fragment are dropped whole: a credential parameter can have
// any name (passwd, pat, jwt, a ";"-separated pair…).
// A value with "://" that url.Parse rejects (a stray %, #, / or space in a
// password) is never returned as it came: everything between "://" and the
// last "@" of its authority is cut. Values without "://" lose any
// "user:password@" prefix (scp-style bot:pw@host:owner/repo.git too); a plain
// git@host:owner/repo.git (a username, no password) and local paths are
// returned unchanged. A "helper::url" remote (ext::https://…) is redacted on
// the url after the "::".
func RedactRemoteURL(raw string) string {
	if i := strings.Index(raw, "://"); i < 0 {
		return redactSchemeless(raw)
	} else if j := strings.Index(raw[:i], "::"); j >= 0 {
		return raw[:j+2] + RedactRemoteURL(raw[j+2:])
	}
	u, err := neturl.Parse(raw)
	// An "@" left in the path means the parser took part of a password for the
	// host or the path (a "/" inside it, or an empty host).
	if err != nil || strings.Contains(u.Path, "@") || strings.Contains(u.RawPath, "@") {
		return redactUnparsable(raw)
	}
	scheme := strings.ToLower(u.Scheme)
	if u.User != nil {
		if scheme == "http" || scheme == "https" || strings.HasSuffix(scheme, "+http") || strings.HasSuffix(scheme, "+https") {
			u.User = nil
		} else if _, hasPassword := u.User.Password(); hasPassword {
			u.User = neturl.User(u.User.Username())
		}
	}
	u.RawQuery, u.ForceQuery = "", false
	u.Fragment, u.RawFragment = "", ""
	return u.String()
}

var schemePrefix = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]+:`)

// redactSchemeless handles a value with no "://": a scheme-relative
// "//user:pass@host/x" and "https:user:pass@host/x" carry userinfo and lose
// it; scp-style git@host:owner/repo.git (the colon follows the "@"), local
// paths and single-letter drive prefixes have none and are returned unchanged.
func redactSchemeless(raw string) string {
	v := strings.TrimSpace(raw)
	prefix := ""
	switch {
	case strings.HasPrefix(v, "//"):
		prefix = "//"
	case schemePrefix.MatchString(v):
		prefix = v[:strings.Index(v, ":")+1]
	}
	if prefix != "" {
		rest := v[len(prefix):]
		if first := strings.Index(rest, "@"); first >= 0 && strings.Contains(rest[:first], ":") {
			return prefix + stripFragment(afterUserinfo(rest))
		}
	}
	// "user:password@host:path": the first colon comes before the first "@".
	// A single letter before it is a drive ("C:\dir@x"), a "/" or "\\" a path.
	at, colon := strings.Index(v, "@"), strings.Index(v, ":")
	if at < 0 || colon < 1 || colon > at {
		return raw
	}
	if name := v[:colon]; len(name) == 1 || strings.ContainsAny(name, `/\`) {
		return raw
	}
	return stripFragment(afterUserinfo(v))
}

// afterUserinfo drops what precedes the last "@" that comes before the query.
func afterUserinfo(rest string) string {
	head := rest
	if q := strings.Index(head, "?"); q >= 0 {
		head = head[:q]
	}
	if at := strings.LastIndex(head, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	return rest
}

// stripFragment drops the fragment and the query string.
func stripFragment(s string) string {
	if i := strings.IndexAny(s, "#?"); i >= 0 {
		return s[:i]
	}
	return s
}

// redactUnparsable handles a URL-looking value whose userinfo the parser cannot
// be trusted with (net/url refuses it, or a "/" or "@" in the password moved
// part of it into the host or path). It cuts what sits between "://" and the
// last "@" before the query; the credential may itself hold "/", "#" or "?",
// so the last "@" is what separates userinfo from host. Credential-looking
// query parameters and the fragment are dropped too.
func redactUnparsable(raw string) string {
	raw = strings.TrimSpace(raw)
	i := strings.Index(raw, "://")
	if i < 0 {
		return raw
	}
	head, rest := raw[:i+3], afterUserinfo(raw[i+3:])
	return head + stripFragment(rest)
}

// RedactRemoteText replaces every occurrence of the remote in text with its
// redacted form, so git output that echoes the URL cannot carry its
// credentials either. git echoes a URL in several shapes: as given, without
// its userinfo but with the query kept, already stripped, or cut at the host
// ("could not read Password for 'http://TOKEN@host'"): the first two are
// replaced whole, then each part of the userinfo (the token-only user, the
// password, with its percent-decoded form) wherever it still appears. A remote
// that is a name rather than a URL has nothing to replace here: callers that
// only hold the name use scrubRepoOutput. Text that never held the remote is
// returned unchanged.
func RedactRemoteText(text, remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return text
	}
	red := RedactRemoteURL(remote)
	if red == remote {
		return text
	}
	forms := []string{remote}
	if i := strings.Index(remote, "://"); i >= 0 {
		if noUser := remote[:i+3] + afterUserinfo(remote[i+3:]); noUser != remote && noUser != red {
			forms = append(forms, noUser)
		}
	}
	for _, f := range forms {
		text = strings.ReplaceAll(text, f, red)
	}
	return scrubUserinfo(text, remote)
}

// minBareSecret is the shortest userinfo part replaced wherever it appears;
// shorter ones would mangle ordinary words, and are replaced only in their
// "part@" form.
const minBareSecret = 4

// userinfoOf returns the raw userinfo of an authority-bearing URL ("" when it
// has none). The credential may hold "@" or "/", so when net/url cannot be
// trusted with the value the last "@" before the query splits userinfo from
// host, as RedactRemoteURL does.
func userinfoOf(raw string) string {
	i := strings.Index(raw, "://")
	if i < 0 {
		return ""
	}
	if j := strings.Index(raw[:i], "::"); j >= 0 {
		return userinfoOf(raw[j+2:])
	}
	rest := raw[i+3:]
	// The userinfo ends at the last "@" of the authority (up to the first "/",
	// "?" or "#"): an "@" further right belongs to the path (GitLab's
	// "/group/@sub/repo").
	auth := rest
	if k := strings.IndexAny(auth, "/?#"); k >= 0 {
		auth = auth[:k]
	}
	if at := strings.LastIndex(auth, "@"); at >= 0 {
		return auth[:at]
	}
	// No "@" in the authority: a "/" inside the password may have moved it
	// into the path ("user:pa/ss@host"); the last "@" before the query then
	// separates userinfo from host, as RedactRemoteURL reads it.
	head := rest
	if q := strings.Index(head, "?"); q >= 0 {
		head = head[:q]
	}
	if at := strings.LastIndex(head, "@"); at >= 0 {
		return rest[:at]
	}
	return ""
}

func unescaped(v string) string {
	if d, err := neturl.PathUnescape(v); err == nil {
		return d
	}
	return v
}

// scrubUserinfo removes the userinfo of remote from text: "userinfo@" goes
// whole, and the user (http(s) only: an ssh user is not a secret) and the
// password go wherever they appear, raw and percent-decoded.
func scrubUserinfo(text, remote string) string {
	ui := userinfoOf(remote)
	if ui == "" {
		return text
	}
	user, pass, hasPass := strings.Cut(ui, ":")
	secrets := []string{ui}
	if hasPass {
		secrets = append(secrets, pass)
	}
	scheme := strings.ToLower(remote[:strings.Index(remote, "://")])
	httpLike := strings.HasSuffix(scheme, "http") || strings.HasSuffix(scheme, "https")
	if httpLike && user != "" {
		if hasPass {
			// A user name is no secret by itself and is often a common word
			// ("github", "oauth2"): it goes only where it is a userinfo.
			for _, f := range []string{user, unescaped(user)} {
				text = strings.ReplaceAll(text, "://"+f+"@", "://")
				text = strings.ReplaceAll(text, "://"+f+":", "://")
			}
		} else {
			// Token-only: the user is the token.
			secrets = append(secrets, user)
		}
	}
	var all []string
	for _, v := range secrets {
		forms := []string{v}
		if d, err := neturl.PathUnescape(v); err == nil && d != v {
			forms = append(forms, d)
		}
		for _, f := range forms {
			all = append(all, f)
			// git cuts a userinfo at an "@" or "/" inside the password and
			// echoes the tail ("http://tail@host"): every tail is a secret.
			for i := 0; i < len(f); i++ {
				if f[i] == '@' || f[i] == '/' {
					all = append(all, f[i+1:])
				}
			}
		}
	}
	// Longest first, so a whole userinfo goes before its parts.
	sort.Slice(all, func(a, b int) bool { return len(all[a]) > len(all[b]) })
	for _, v := range all {
		if v == "" {
			continue
		}
		text = strings.ReplaceAll(text, v+"@", "")
		if len(v) >= minBareSecret {
			text = strings.ReplaceAll(text, v, "***")
		}
	}
	return text
}

// repoRemoteURLs lists every URL (fetch and push, insteadOf rewrites applied)
// of the repository at dir's remotes. ok is false when git cannot say.
func repoRemoteURLs(dir string) (urls []string, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := procattr.Hide(exec.CommandContext(ctx, "git", "-C", dir, "remote", "-v"))
	cmd.Env = gitEnv(nil)
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	for _, line := range strings.Split(string(out), "\n") {
		_, rest, ok := strings.Cut(strings.TrimRight(line, "\r"), "\t")
		if !ok {
			continue
		}
		if k := strings.LastIndex(rest, " ("); k >= 0 {
			rest = rest[:k]
		}
		if rest = strings.TrimSpace(rest); rest != "" {
			urls = append(urls, rest)
		}
	}
	return urls, true
}

// anyUserinfo matches "://" followed by everything up to the last "@" of the
// same whitespace-free token: a userinfo, whatever it holds.
var anyUserinfo = regexp.MustCompile(`://[^\s'"<>]*@`)

// redactAnyUserinfo drops the userinfo of every URL in text, without knowing
// the remotes: the fail-closed form of the scrub. It also drops an ssh user,
// which is not a secret: only used when the remote list is unavailable.
func redactAnyUserinfo(text string) string {
	return anyUserinfo.ReplaceAllString(text, "://")
}

// scrubRepoOutput redacts text, git output of a command run in dir, of the
// credentials of every remote the repository has. A command is named by a
// remote's name ("origin"), which is not what git echoes: the URL is, and with
// it a token. Callers use it on the failure path only, the lookup costs a git
// process.
func scrubRepoOutput(dir, text string) string {
	if text == "" || !strings.Contains(text, "@") && !strings.Contains(text, "://") {
		return text
	}
	urls, ok := repoRemoteURLs(dir)
	if !ok {
		// git could not list the remotes: fail closed, drop every userinfo.
		return redactAnyUserinfo(text)
	}
	for _, u := range urls {
		text = RedactRemoteText(text, u)
	}
	return text
}

// scrubWriter forwards what git writes to w with the remote redacted. Output
// is cut into lines at "\n" or "\r" (git's progress rewrites its line with
// "\r"), so a URL is never split across two writes; Flush emits the tail.
type scrubWriter struct {
	w      io.Writer
	remote string
	buf    []byte
}

func (s *scrubWriter) Write(p []byte) (int, error) {
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexAny(s.buf, "\r\n")
		if i < 0 {
			return len(p), nil
		}
		line := s.buf[:i+1]
		if _, err := io.WriteString(s.w, RedactRemoteText(string(line), s.remote)); err != nil {
			return len(p), err
		}
		s.buf = s.buf[i+1:]
	}
}

// Flush writes the unterminated tail.
func (s *scrubWriter) Flush() {
	if len(s.buf) > 0 {
		_, _ = io.WriteString(s.w, RedactRemoteText(string(s.buf), s.remote))
		s.buf = nil
	}
}

// redactedErr returns err unchanged unless its text carries the raw remote, in
// which case the redacted text is returned (the error chain is kept for
// errors.Is/As).
func redactedErr(err error, remote string) error {
	if err == nil || remote == "" || !strings.Contains(err.Error(), remote) {
		return err
	}
	return &redactedError{msg: RedactRemoteText(err.Error(), remote), err: err}
}

type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }
