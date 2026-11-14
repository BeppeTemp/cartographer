package gitx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A remote addressed by its name ("origin") has git echo its URL in the
// failure; the credentials in that URL never reach the returned error, whether
// they sit in a token-only userinfo or in a password holding "@" or "/".
func TestNamedRemoteFailuresRedactStoredCredentials(t *testing.T) {
	cases := []struct {
		name, url string
		secrets   []string
	}{
		{"token-only", "http://tok3n-Abc123@127.0.0.1:1/x.git", []string{"tok3n-Abc123"}},
		{"password-with-at", "http://user:pa@ss-w0rd9@127.0.0.1:1/x.git", []string{"pa@ss-w0rd9", "ss-w0rd9"}},
		{"password-with-slash", "http://user:pa/ss-w0rd9@127.0.0.1:1/x.git", []string{"pa/ss-w0rd9", "ss-w0rd9"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			mustGit(t, dir, "init", "-q", "-b", "main")
			mustGit(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
			mustGit(t, dir, "remote", "add", "origin", c.url)
			if err := os.WriteFile(filepath.Join(dir, ".keep"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			errs := map[string]error{
				"push":   Push(dir, "origin", "main"),
				"fetch":  Fetch(dir, "origin"),
				"pull":   PullRebaseAutostash(dir, "origin", "main"),
				"push-u": PushSetUpstream(dir, "origin", "main"),
			}
			_, errs["ls-remote"] = LsRemote(dir, "origin")
			for op, err := range errs {
				if err == nil {
					t.Fatalf("%s to an unreachable remote succeeded", op)
				}
				for _, s := range c.secrets {
					if strings.Contains(err.Error(), s) {
						t.Errorf("%s error leaks %q: %v", op, s, err)
					}
				}
			}
			out, err := runGit(dir, "push", "origin", "main")
			if err == nil {
				t.Fatal("push succeeded")
			}
			for _, s := range c.secrets {
				if strings.Contains(out, s) {
					t.Errorf("runGit output leaks %q: %s", s, out)
				}
			}
		})
	}
}

func TestRedactRemoteTextScrubsUserinfoParts(t *testing.T) {
	cases := []struct{ remote, text, leak string }{
		{"http://tok3n-Abc123@host.example/r.git", "could not read Password for 'http://tok3n-Abc123@host.example'", "tok3n-Abc123"},
		{"https://user:pa%40ss-w0rd9@host.example/r.git", "bad pa@ss-w0rd9 here", "ss-w0rd9"},
		{"http://user:pa@ss-w0rd9@host.example/r.git", "Password for 'http://user:pa@ss-w0rd9@host.example'", "ss-w0rd9"},
		{"http://user:pa/ss-w0rd9@host.example/r.git", "Password for 'http://user:pa/ss-w0rd9@host.example'", "ss-w0rd9"},
	}
	for _, c := range cases {
		if got := RedactRemoteText(c.text, c.remote); strings.Contains(got, c.leak) {
			t.Errorf("RedactRemoteText(%q, %q) = %q, still holds %q", c.text, c.remote, got, c.leak)
		}
	}
}

// An "@" in the path (GitLab subgroups) is not the end of the userinfo.
func TestRedactRemoteTextAtSignInPath(t *testing.T) {
	remote := "https://SECRETTOKEN@host.example.com/g/@sub/repo.git"
	text := "could not read Password for 'https://SECRETTOKEN@host.example.com'"
	if got := RedactRemoteText(text, remote); strings.Contains(got, "SECRETTOKEN") {
		t.Errorf("token leaks: %q", got)
	}
	remote = "https://SECRETUSER:SECRETPASS@host.example.com/g/@sub/repo.git"
	text = "fatal: Authentication failed for 'https://SECRETUSER:SECRETPASS@host.example.com/g/@sub/repo.git'"
	if got := RedactRemoteText(text, remote); strings.Contains(got, "SECRETPASS") || strings.Contains(got, "SECRETUSER") {
		t.Errorf("credential leaks: %q", got)
	}
}

// A user name that is a common word is removed from the userinfo only, not
// from the host, the path or a branch name in the same output.
func TestRedactRemoteTextKeepsUsernameElsewhere(t *testing.T) {
	remote := "https://oauth2:Pw-s3cret-9@oauth2.example.com/oauth2/repo.git"
	text := "fatal: unable to access 'https://oauth2:Pw-s3cret-9@oauth2.example.com/oauth2/repo.git/' on branch oauth2-fix"
	got := RedactRemoteText(text, remote)
	if strings.Contains(got, "Pw-s3cret-9") || strings.Contains(got, "://oauth2:") || strings.Contains(got, "://oauth2@") {
		t.Errorf("credential left: %q", got)
	}
	for _, keep := range []string{"oauth2.example.com", "/oauth2/repo.git", "oauth2-fix"} {
		if !strings.Contains(got, keep) {
			t.Errorf("%q was rewritten: %q", keep, got)
		}
	}
}

// A push whose remote argument is a URL with credentials never carries them in
// its error.
func TestPushErrorRedactsRemoteArgument(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q", "-b", "main")
	mustGit(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	remote := "http://user:s3cr3t-Pw0rd@127.0.0.1:1/x.git"
	for op, err := range map[string]error{
		"push":   Push(dir, remote, "main"),
		"push-u": PushSetUpstream(dir, remote, "main"),
	} {
		if err == nil {
			t.Fatalf("%s to an unreachable remote succeeded", op)
		}
		if strings.Contains(err.Error(), "s3cr3t-Pw0rd") {
			t.Errorf("%s error leaks the credential: %v", op, err)
		}
	}
}

// When git cannot list the remotes, the scrub still drops every userinfo.
func TestScrubRepoOutputFailsClosedWithoutRemoteList(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	text := "fatal: unable to access 'http://user:s3cr3t-Pw0rd@example.com/x.git/': boom\nhttp://tail@example.com"
	got := scrubRepoOutput(dir, text)
	for _, s := range []string{"s3cr3t-Pw0rd", "user:", "tail@"} {
		if strings.Contains(got, s) {
			t.Errorf("scrub kept %q: %s", s, got)
		}
	}
	if !strings.Contains(got, "example.com") {
		t.Errorf("scrub dropped the host: %s", got)
	}
}
