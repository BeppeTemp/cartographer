package gitx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A clone of a locator written by a KB writer carries none of the operator's
// identity: the credential helper of the global git config is not run on a 401,
// and a redirect to another host is not followed (D1262).
func TestCloneAnonymousCarriesNoOperatorIdentityAndFollowsNoRedirect(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	home := t.TempDir()
	marker := filepath.Join(home, "helper-ran")
	cfg := "[credential]\n\thelper = \"!f() { touch " + filepath.ToSlash(marker) + "; echo username=u; echo password=p; }; f\"\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

	var auth atomic.Int32
	secured := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Add(1)
		w.Header().Set("WWW-Authenticate", `Basic realm="r"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer secured.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Control: the plain clone does consult the operator's helper, so the test can fail.
	_ = Clone(ctx, secured.URL+"/r.git", filepath.Join(t.TempDir(), "plain"), "GIT_ALLOW_PROTOCOL=http")
	if _, err := os.Stat(marker); err != nil {
		t.Skipf("this git does not run the credential helper here (%v): the control is void", err)
	}
	_ = os.Remove(marker)

	if err := cloneAnonymous(ctx, secured.URL+"/r.git", filepath.Join(t.TempDir(), "anon"), "GIT_ALLOW_PROTOCOL=http"); err == nil {
		t.Fatal("clone of a 401 server succeeded")
	}
	if auth.Load() < 2 { // the control made one or more, the anonymous clone at least one
		t.Fatal("the anonymous clone never reached the server")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the operator's credential helper ran")
	}

	var hit atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit.Add(1) }))
	defer target.Close()
	var redirects atomic.Int32
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirects.Add(1)
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer redirect.Close()
	_ = cloneAnonymous(ctx, redirect.URL+"/r.git", filepath.Join(t.TempDir(), "redir"), "GIT_ALLOW_PROTOCOL=http")
	if redirects.Load() == 0 {
		t.Fatal("the redirecting server was never asked: the test proves nothing")
	}
	if hit.Load() != 0 {
		t.Fatal("a redirect was followed")
	}
}

func TestAnonymousEnvOverridesTheProcessEnvironment(t *testing.T) {
	t.Setenv("GIT_ASKPASS", "/bin/evil")
	env := gitEnv(AnonymousEnv())
	last := map[string]string{}
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		last[k] = v
	}
	if last["GIT_ASKPASS"] != "" || last["GIT_CONFIG_GLOBAL"] != os.DevNull || last["GIT_CONFIG_NOSYSTEM"] != "1" ||
		last["GIT_CONFIG_KEY_1"] != "http.followRedirects" || last["GIT_CONFIG_VALUE_1"] != "false" {
		t.Fatalf("env = %v", last)
	}
}
