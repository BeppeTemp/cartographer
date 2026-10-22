// Package gitxtest is a git smart-HTTP server for tests (D996): the real
// git-http-backend behind basic authentication, in process, so a test meets
// what a forge's HTTPS endpoint does (a 401, a credential helper, a pre-receive
// hook's "remote:" lines) without a network. Only _test.go files import it.
package gitxtest

import (
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// HTTPRemote serves a new empty bare repository, on branch main, over smart
// HTTP with basic authentication for user and password. It returns the clone
// URL and the bare repository's path (to install a hook in, or to read what a
// push left). The test is skipped when git has no http-backend.
func HTTPRemote(t testing.TB, user, password string) (url, bare string) {
	t.Helper()
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Skipf("git not available: %v", err)
	}
	backend := filepath.Join(strings.TrimSpace(string(out)), "git-http-backend")
	if _, err := exec.LookPath(backend); err != nil {
		if _, serr := os.Stat(backend + ".exe"); serr != nil {
			t.Skip("git http-backend not found")
		}
	}

	root := t.TempDir()
	bare = filepath.Join(root, "remote.git")
	for _, args := range [][]string{
		{"init", "--bare", bare},
		{"-C", bare, "symbolic-ref", "HEAD", "refs/heads/main"},
		{"-C", bare, "config", "http.receivepack", "true"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	h := &cgi.Handler{
		Path: backend,
		Env:  []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"},
		// The CGI child gets only what is listed: git and its hooks need a PATH,
		// and on Windows the system root.
		InheritEnv: []string{"PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "TMPDIR", "TEMP", "TMP"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != user || p != password {
			w.Header().Set("WWW-Authenticate", `Basic realm="forge"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/remote.git", bare
}

// TokenEnv is the git environment that makes a credential helper answer with
// user "token" and the content of tokenFile: the three GIT_CONFIG_* variables
// of cmd/cartographer's gitTokenCredentialEnv, which that package's own test
// checks against a server (bootstrap_test.go). Kept in step by hand.
func TokenEnv(tokenFile string) []string {
	escaped := strings.ReplaceAll(tokenFile, "'", `'\''`)
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=credential.helper",
		`GIT_CONFIG_VALUE_0=!f() { echo username=token; echo "password=$(cat '` + escaped + `')"; }; f`,
	}
}

// IsolatedEnv keeps the machine's own git configuration out of a test: its
// credential helper (a keychain) would answer for a missing token, and store a
// test token. Not t.Setenv, which a parallel test cannot use.
func IsolatedEnv() []string {
	return []string{"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		// no identity guessed from the passwd entry and hostname
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=user.useConfigOnly", "GIT_CONFIG_VALUE_0=true"}
}
