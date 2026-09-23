package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sourceDir is the frontend tree this package's bundle was built from.
const sourceDir = "../../web"

// TestBundleMatchesSource is the gate that keeps a committed generated artifact
// from silently going stale. It is pure Go on purpose: a contributor changing
// Go code must not need a Node toolchain to run `make gate`, but they must
// still be told when a colleague's frontend change was committed without its
// rebuilt bundle.
func TestBundleMatchesSource(t *testing.T) {
	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		t.Skip("frontend sources are not present in this tree")
	}
	provenance, err := ReadProvenance()
	if err != nil {
		t.Fatalf("the embedded bundle carries no provenance: %v — run `make web`", err)
	}
	hash, files, err := SourceHash(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	if hash != provenance.SourceHash {
		t.Fatalf("the embedded UI bundle is stale: web/ hashes to %s (%d files) but the bundle "+
			"was built from %s (%d files).\nRun `make web` and commit internal/webui/dist.",
			hash[:12], files, short(provenance.SourceHash), provenance.Files)
	}
}

func short(hash string) string {
	if len(hash) < 12 {
		return hash
	}
	return hash[:12]
}

func TestSourceHashIgnoresBuildOutputAndDependencies(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/app.ts", "const a = 1;")
	write("package.json", "{}")

	base, files, err := SourceHash(root)
	if err != nil {
		t.Fatal(err)
	}
	if files != 2 {
		t.Fatalf("counted %d files, want 2", files)
	}

	// Neither the install tree nor the build output may move the hash: they
	// are outputs, and hashing them would make the check circular.
	write("node_modules/left-pad/index.js", "module.exports = 1;")
	write("dist/assets/index-abc.js", "console.log(1)")
	after, files, err := SourceHash(root)
	if err != nil {
		t.Fatal(err)
	}
	if after != base || files != 2 {
		t.Fatalf("node_modules/ or dist/ changed the source hash (%d files)", files)
	}

	// A content change must move it.
	write("src/app.ts", "const a = 2;")
	changed, _, err := SourceHash(root)
	if err != nil {
		t.Fatal(err)
	}
	if changed == base {
		t.Fatal("editing a source file did not change the hash")
	}

	// So must a rename, even with identical bytes: the bundle was built from a
	// tree that no longer exists.
	if err := os.Rename(filepath.Join(root, "src/app.ts"), filepath.Join(root, "src/main.ts")); err != nil {
		t.Fatal(err)
	}
	renamed, _, err := SourceHash(root)
	if err != nil {
		t.Fatal(err)
	}
	if renamed == changed {
		t.Fatal("renaming a source file did not change the hash")
	}
}

func TestBundleCarriesAShellAndHashedAssets(t *testing.T) {
	assets, err := Assets()
	if err != nil {
		t.Fatal(err)
	}
	shell, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("no shell in the embedded bundle: %v", err)
	}
	if !strings.Contains(string(shell), "/ui/assets/") {
		t.Errorf("the shell does not reference /ui/-based assets; check vite's base: %s", shell)
	}
	entries, err := fs.ReadDir(assets, "assets")
	if err != nil || len(entries) == 0 {
		t.Fatalf("the embedded bundle has no assets: %v", err)
	}
	// Source maps would double the released binary for something only a
	// developer with the sources can use.
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".map") {
			t.Errorf("source map %s shipped in the bundle", entry.Name())
		}
	}
}

func newUIServer(t *testing.T) http.Handler {
	t.Helper()
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestHandlerServesTheShellForClientRoutes(t *testing.T) {
	handler := newUIServer(t)
	for _, path := range []string{MountPath, MountPath + "anything", MountPath + "deep/client/route"} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200", path, rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "<div id=\"root\">") {
			t.Errorf("%s did not serve the shell", path)
		}
		if got := rr.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s: shell Cache-Control %q, want no-cache", path, got)
		}
	}
}

func TestHandlerCachesHashedAssetsForever(t *testing.T) {
	assets, err := Assets()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(assets, "assets")
	if err != nil || len(entries) == 0 {
		t.Fatal("no assets to check")
	}
	handler := newUIServer(t)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, MountPath+"assets/"+entries[0].Name(), nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("asset: status %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("hashed asset Cache-Control %q, want immutable", got)
	}
}

func TestHandlerSetsAStrictContentSecurityPolicy(t *testing.T) {
	handler := newUIServer(t)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, MountPath, nil))

	csp := rr.Header().Get("Content-Security-Policy")
	for _, directive := range []string{
		"default-src 'self'",
		"script-src 'self'",
		"connect-src 'self'",
		"object-src 'none'",
		"frame-ancestors 'none'",
	} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP is missing %q: %s", directive, csp)
		}
	}
	// The UI runs no worker: nothing justifies admitting blob: code.
	if strings.Contains(csp, "blob:") {
		t.Errorf("CSP admits blob: code, which no part of the UI needs: %s", csp)
	}
	// The UI ships everything it uses, so nothing justifies either of these.
	if strings.Contains(csp, "unsafe-eval") {
		t.Errorf("CSP allows eval: %s", csp)
	}
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Errorf("CSP allows inline script: %s", csp)
	}
	if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options %q", got)
	}
}

func TestHandlerIsReadOnly(t *testing.T) {
	handler := newUIServer(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(method, MountPath, nil))
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d, want 405", method, rr.Code)
		}
	}
}

// A traversal can never leave the bundle, and the build manifest is not part
// of what a browser may fetch.
func TestHandlerDoesNotEscapeTheBundleOrServeItsManifest(t *testing.T) {
	handler := newUIServer(t)
	for _, path := range []string{
		MountPath + "../../etc/passwd",
		MountPath + "..%2f..%2fetc%2fpasswd",
		MountPath + ProvenanceFile,
		MountPath + "..%2f..%2f" + ProvenanceFile,
	} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200", path, rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "<div id=\"root\">") {
			t.Errorf("%s served something that is not the shell: %s", path, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "source_hash") {
			t.Errorf("%s served the build manifest", path)
		}
	}
}

// RedirectRoot sits outside the auth chain, so a browser that typed the bare
// address reaches the UI without already holding a token. It must redirect "/"
// and touch nothing else.
func TestRedirectRootOnlyTouchesTheRoot(t *testing.T) {
	var reached string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = r.URL.Path
		w.WriteHeader(http.StatusTeapot)
	})
	handler := RedirectRoot(inner)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusMovedPermanently {
		t.Errorf("/: status %d, want 301", rr.Code)
	}
	if got := rr.Header().Get("Location"); got != MountPath {
		t.Errorf("/: Location %q, want %q", got, MountPath)
	}
	if reached != "" {
		t.Errorf("/ reached the wrapped handler at %q", reached)
	}

	for _, path := range []string{"/mcp", "/health", "/api/ui/v1/kbs", "/ui/"} {
		reached = ""
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if reached != path {
			t.Errorf("%s did not pass through (reached %q)", path, reached)
		}
	}

	// A POST to "/" is not a browser typing an address: pass it through rather
	// than turning a write into a redirect.
	reached = ""
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/", nil))
	if reached != "/" {
		t.Errorf("POST / was redirected instead of passed through")
	}
}
