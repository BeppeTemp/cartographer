package mcpbridge

import (
	"errors"
	"fmt"
	"github.com/BeppeTemp/cartographer/internal/client"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

// TestMain: see gitxtest.QuietGit. It also builds the shared "elsewhere"
// binding once, before any test runs (see withBinding).
func TestMain(m *testing.M) {
	gitxtest.QuietGit()
	// The stub servers of these tests are not /health-aware; the check itself is
	// pinned by TestBridgeSendsNoTokenToAForeignListener.
	verifyServer = func(string, bool, *http.Client) error { return nil }
	if err := setupElsewhere(); err != nil {
		// A bare panic in a fixture loses its message when the binary dies
		// under a parallel gate (#1847): name the failure.
		fmt.Fprintln(os.Stderr, "mcpbridge tests: cannot prepare the elsewhere binding:", err)
		if elsewhereDir != "" {
			os.RemoveAll(elsewhereDir)
		}
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(elsewhereDir)
	os.Exit(code)
}

// elsewhereDir is a folder no test runs in, bound for every provider by
// withBinding: the realistic shape of a bridge session in an unbound folder
// (a provider with no binding at all gets the HTTP-equivalent scope instead).
// elsewhereBinding is its binding, built once by a real BindWorkspace so the
// shape stays what production builds.
var (
	elsewhereDir     string
	elsewhereBinding clientconfig.WorkspaceBinding
)

// setupElsewhere creates elsewhereDir and its binding once. withBinding must
// not touch the filesystem or spawn git on every call: a transient error on
// that path under load (seen on Windows, #1847) would fail an unrelated test.
func setupElsewhere() error {
	d, err := os.MkdirTemp("", "bridge-elsewhere-")
	if err != nil {
		return err
	}
	elsewhereDir = d
	scratch := &clientconfig.Config{}
	if err := scratch.BindWorkspace("claude", d, []string{"kb-a"}); err != nil {
		return err
	}
	list := scratch.Workspaces["claude"]
	if len(list) != 1 {
		return fmt.Errorf("expected one binding, got %d", len(list))
	}
	elsewhereBinding = list[0]
	return nil
}

// withBinding adds the shared binding to the four providers of cfg, replacing
// an entry with the same path as BindWorkspace does. No filesystem, no git.
func withBinding(cfg *clientconfig.Config) *clientconfig.Config {
	if cfg.Workspaces == nil {
		cfg.Workspaces = map[string][]clientconfig.WorkspaceBinding{}
	}
	for _, p := range []string{"claude", "codex", "kiro", "opencode"} {
		b := elsewhereBinding
		b.KBs = append([]string{}, elsewhereBinding.KBs...)
		b.Sources = append([]string(nil), elsewhereBinding.Sources...)
		list := cfg.Workspaces[p]
		replaced := false
		for i := range list {
			if list[i].Path == b.Path {
				list[i] = b
				replaced = true
				break
			}
		}
		if !replaced {
			list = append(list, b)
		}
		cfg.Workspaces[p] = list
	}
	return cfg
}

// TestWithBindingNeedsNoFilesystem pins #1847: withBinding must work even if
// the shared directory is gone and the temp directory is unreachable. The
// directory is not touched (a rename under TEMP can itself fail on Windows
// while an indexer holds a handle): elsewhereDir is pointed at a path that
// does not exist, so any code re-deriving the binding from it would fail.
func TestWithBindingNeedsNoFilesystem(t *testing.T) {
	saved := elsewhereDir
	t.Cleanup(func() { elsewhereDir = saved })
	missing := filepath.Join(t.TempDir(), "missing")
	elsewhereDir = missing
	for _, k := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(k, missing)
	}

	cfg := withBinding(&clientconfig.Config{})
	for _, p := range []string{"claude", "codex", "kiro", "opencode"} {
		list := cfg.Workspaces[p]
		if len(list) != 1 || list[0].Path != elsewhereBinding.Path || len(list[0].KBs) != 1 || list[0].KBs[0] != "kb-a" {
			t.Fatalf("%s: bindings = %+v", p, list)
		}
	}
	// A second call replaces the entry instead of duplicating it, and the
	// copies do not share the KBs slice.
	withBinding(cfg)
	if n := len(cfg.Workspaces["kiro"]); n != 1 {
		t.Fatalf("second call left %d bindings", n)
	}
	cfg.Workspaces["claude"][0].KBs[0] = "changed"
	if cfg.Workspaces["codex"][0].KBs[0] != "kb-a" || elsewhereBinding.KBs[0] != "kb-a" {
		t.Fatal("bindings share the KBs slice")
	}
}

// A listener whose /health is not a Cartographer server's never sees the
// token, whatever the bridge forwards.
func TestBridgeSendsNoTokenToAForeignListener(t *testing.T) {
	verifyServer = client.VerifyServer
	t.Cleanup(func() { verifyServer = func(string, bool, *http.Client) error { return nil } })
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	b := &Bridge{Cfg: &clientconfig.Config{ServerURL: srv.URL + "/mcp"}, Token: "sekret"}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", nil)
	if err := b.authorize(req); !errors.Is(err, client.ErrNotCartographerServer) {
		t.Fatalf("authorize = %v, want ErrNotCartographerServer", err)
	}
	if req.Header.Get("Authorization") != "" {
		t.Error("the request carries the token")
	}
	for i, h := range seen {
		if h != "" {
			t.Errorf("request %d carried %q to a foreign listener", i, h)
		}
	}
}
