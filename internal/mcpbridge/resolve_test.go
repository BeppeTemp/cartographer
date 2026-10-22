package mcpbridge

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
)

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	// Canonical, like the binding table: t.TempDir can sit behind a symlink.
	c, err := clientconfig.CanonicalWorkspacePath(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func baseConfig() *clientconfig.Config {
	return &clientconfig.Config{
		ServerName: "cartographer", ServerURL: "https://example.com/mcp", ServerRoutedPath: "/mcp/routed",
		KnownKBs: []string{"kb-a", "kb-b", "kb-c"},
	}
}

func bind(t *testing.T, cfg *clientconfig.Config, dir string, kbs ...string) {
	t.Helper()
	if err := cfg.BindWorkspace("claude", dir, kbs); err != nil {
		t.Fatal(err)
	}
}

// The resolution matrix of D737: bound, nested, unbound, --no-kb, unmounted.
func TestResolveMatrix(t *testing.T) {
	root := mkdir(t, t.TempDir())
	area := mkdir(t, root, "area")
	repo := mkdir(t, area, "repo")
	other := mkdir(t, root, "other")

	cfg := baseConfig()
	bind(t, cfg, root, "kb-a")           // a binding on the outer folder
	bind(t, cfg, area, "kb-b", "kb-c")   // a nested one wins below it
	bind(t, cfg, mkdir(t, root, "none")) // --no-kb
	ghost := mkdir(t, root, "ghost")
	bind(t, cfg, ghost, "gone") // every bound KB is unmounted

	cases := []struct {
		name string
		dir  string
		want []string
		all  bool
		none bool
	}{
		{"outer binding", other, []string{"kb-a"}, false, false},
		{"nested binding wins", repo, []string{"kb-b", "kb-c"}, false, false},
		{"bound folder itself", area, []string{"kb-b", "kb-c"}, false, false},
		{"no-kb is no KB, never every KB", filepath.Join(root, "none"), nil, false, true},
		{"unmounted KB is no KB", ghost, nil, false, true},
		{"unbound folder is the provider default", t.TempDir(), nil, true, false},
	}
	readOnly := map[string]bool{"unbound folder is the provider default": true}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := Resolve(cfg, "claude", c.dir)
			if s.All != c.all || s.None() != c.none || !reflect.DeepEqual(s.KBs, c.want) {
				t.Fatalf("scope = %+v, want kbs=%v all=%v none=%v", s, c.want, c.all, c.none)
			}
			if s.ReadOnly != readOnly[c.name] {
				t.Fatalf("ReadOnly = %v, want %v (only an unbound folder is read-only)", s.ReadOnly, readOnly[c.name])
			}
			if c.none && s.Query() != "" {
				t.Fatalf("an empty set must never produce a kbs= query, got %q", s.Query())
			}
		})
	}
}

func TestResolveUnboundUsesTheProviderBinding(t *testing.T) {
	cfg := baseConfig()
	cfg.Clients = map[string]clientconfig.ClientBinding{"claude": {KBs: []string{"kb-b"}, KBsSet: true}}
	s := Resolve(cfg, "claude", t.TempDir())
	if s.All || !reflect.DeepEqual(s.KBs, []string{"kb-b"}) {
		t.Fatalf("scope = %+v", s)
	}
	if got := s.Query(); got != "kbs=kb-b&access=read" {
		t.Fatalf("query = %q", got)
	}
}

// A binding whose directory is gone fails closed: no KB, with the reason.
func TestResolveBrokenBindingFailsClosed(t *testing.T) {
	root := mkdir(t, t.TempDir())
	gone := mkdir(t, root, "gone")
	cfg := baseConfig()
	bind(t, cfg, gone, "kb-a")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	s := Resolve(cfg, "claude", gone)
	// The folder itself no longer resolves; a sibling under the bound path is
	// the realistic case, so ask through a path that still canonicalizes.
	if s.Err == nil && !s.None() && !s.All {
		t.Fatalf("scope = %+v", s)
	}
	if s.All {
		t.Fatalf("a broken binding must not fall back to every KB: %+v", s)
	}
}

func TestResolveRootsFallback(t *testing.T) {
	root := mkdir(t, t.TempDir())
	a := mkdir(t, root, "a")
	b := mkdir(t, root, "b")
	plain := mkdir(t, root, "plain")
	cfg := baseConfig()
	bind(t, cfg, a, "kb-a")
	bind(t, cfg, b, "kb-b")

	s, ok := ResolveRoots(cfg, "claude", []string{fileURI(a), fileURI(b), fileURI(plain), "https://example.com/x"})
	if !ok || !reflect.DeepEqual(s.KBs, []string{"kb-a", "kb-b"}) {
		t.Fatalf("union = %+v ok=%v", s, ok)
	}
	// Each KB remembers the root that bound it: its writes take that
	// repository's git identity (D904).
	if want := map[string][]string{"kb-a": {a}, "kb-b": {b}}; !reflect.DeepEqual(s.Dirs, want) {
		t.Errorf("dirs = %v, want %v", s.Dirs, want)
	}
	if s.ReadOnly || strings.Contains(s.Query(), "access") {
		t.Errorf("a root-derived binding is never read-only: %+v", s)
	}
	if _, ok := ResolveRoots(cfg, "claude", []string{fileURI(plain)}); ok {
		t.Fatal("an unbound root must leave the provider scope alone")
	}
	if _, ok := ResolveRoots(cfg, "claude", nil); ok {
		t.Fatal("no roots must leave the provider scope alone")
	}
}

func fileURI(p string) string {
	p = filepath.ToSlash(p)
	if runtime.GOOS == "windows" {
		return "file:///" + p
	}
	return "file://" + p
}

func TestRootPath(t *testing.T) {
	cases := []struct {
		uri  string
		want string
		ok   bool
	}{
		{"file:///home/user/area", filepath.FromSlash("/home/user/area"), true},
		{"file:///C:/Users/user/area", filepath.FromSlash("C:/Users/user/area"), true},
		{"file://localhost/home/user", filepath.FromSlash("/home/user"), true},
		{"https://example.com/x", "", false},
		{"file://", "", false},
	}
	for _, c := range cases {
		got, ok := rootPath(c.uri)
		if ok != c.ok || got != c.want {
			t.Errorf("rootPath(%q) = %q, %v; want %q, %v", c.uri, got, ok, c.want, c.ok)
		}
	}
}

func TestQueryEscapesAndSorts(t *testing.T) {
	s := Scope{KBs: []string{"kb a", "kb-b"}}
	if got := s.Query(); !strings.HasPrefix(got, "kbs=kb+a,kb-b") {
		t.Fatalf("query = %q", got)
	}
}

// The read-only flag survives to the query in every shape, and an empty set is
// still never sent as kbs=.
func TestQueryReadOnly(t *testing.T) {
	cases := []struct {
		s    Scope
		want string
	}{
		{Scope{All: true}, ""},
		{Scope{All: true, ReadOnly: true}, "access=read"},
		{Scope{KBs: []string{"kb-a", "kb-b"}}, "kbs=kb-a,kb-b"},
		{Scope{KBs: []string{"kb-a", "kb-b"}, ReadOnly: true}, "kbs=kb-a,kb-b&access=read"},
		{Scope{ReadOnly: true}, "access=read"},
	}
	for _, c := range cases {
		if got := c.s.Query(); got != c.want {
			t.Errorf("%+v: query = %q, want %q", c.s, got, c.want)
		}
	}
}

// With the provider default read-only, roots that bind nothing keep it.
func TestResolveRootsUnboundKeepsReadOnlyProviderScope(t *testing.T) {
	cfg := baseConfig()
	plain := mkdir(t, t.TempDir())
	if _, ok := ResolveRoots(cfg, "claude", []string{fileURI(plain)}); ok {
		t.Fatal("an unbound root must leave the provider scope alone")
	}
	if s := Resolve(cfg, "claude", plain); !s.ReadOnly || !s.All {
		t.Fatalf("provider scope = %+v, want read-only All", s)
	}
}

// The bridge's header constants are the server's.
func TestAccessConstantsMatchTheServer(t *testing.T) {
	if accessHeader != mcpserver.AccessHeader || accessRead != mcpserver.AccessReadValue {
		t.Fatalf("bridge %q=%q, server %q=%q", accessHeader, accessRead, mcpserver.AccessHeader, mcpserver.AccessReadValue)
	}
}
