package provisioning

// D263: a KB's declared registry drives resolution after `paths:` and the
// repo index, and before "unresolved".

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeClone makes dir a git clone whose origin is remote, as repoindex reads
// it (a .git/config, no git exec).
func fakeClone(t *testing.T, dir, remote string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[remote \"origin\"]\n\turl = " + remote + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

func registryOf(paths, repos map[string]PathDecl) PathRegistry {
	return PathRegistry{Paths: paths, Repos: repos}
}

// The acceptance of D263 WP2: a declared ~-default that exists resolves with
// an empty `paths:`, and lands in the "Local paths" table.
func TestApply_DeclaredDefaultResolvesWithoutPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	m, kbRoot := placeholderKB(t)
	baseDir := t.TempDir()
	opts := clientApplyOptions(kbRoot, baseDir, Lock{})
	opts.Placeholders = map[string][]string{"path:claude-home": {"kb"}}
	opts.PathRegistries = map[string]PathRegistry{"kb": registryOf(map[string]PathDecl{
		"claude-home": {Description: "Claude Code's directory", Default: "~/.claude"},
	}, nil)}

	res, err := Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := filepath.Join(home, ".claude")
	if got := res.NewLock.ResolvedPlaceholders["path:claude-home"]; got != want {
		t.Fatalf("resolved = %q, want %q (unresolved: %v)", got, want, res.NewLock.UnresolvedPlaceholders)
	}
	if content := readInstructions(t, baseDir); !strings.Contains(content, "| `{{path:claude-home}}` | `"+want+"` |") {
		t.Errorf("table must list the default-resolved key:\n%s", content)
	}
	if d := res.NewLock.PlaceholderDecls["path:claude-home"]; d.Description != "Claude Code's directory" || d.Default != "~/.claude" {
		t.Errorf("lock decls = %+v", res.NewLock.PlaceholderDecls)
	}
}

func TestApply_ExplicitPathsWinsOverDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	m, kbRoot := placeholderKB(t)
	opts := clientApplyOptions(kbRoot, t.TempDir(), Lock{})
	opts.Placeholders = map[string][]string{"path:claude-home": {"kb"}}
	opts.Paths = map[string]string{"claude-home": "/srv/elsewhere"}
	opts.PathRegistries = map[string]PathRegistry{"kb": registryOf(map[string]PathDecl{"claude-home": {Description: "d", Default: "~/.claude"}}, nil)}
	res, err := Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.NewLock.ResolvedPlaceholders["path:claude-home"]; got != "/srv/elsewhere" {
		t.Errorf("resolved = %q, want the explicit mapping", got)
	}
}

// A default that does not exist is an unresolved key, not a wrong path, and
// the reason says so. An absolute default — impossible from a current
// server — is never used either.
func TestApply_MissingOrUnanchoredDefaultIsUnresolved(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	abs := t.TempDir() // exists, but is not home-anchored
	m, kbRoot := placeholderKB(t)
	opts := clientApplyOptions(kbRoot, t.TempDir(), Lock{})
	opts.Placeholders = map[string][]string{"path:missing": {"kb"}, "path:absolute": {"kb"}}
	opts.PathRegistries = map[string]PathRegistry{"kb": registryOf(map[string]PathDecl{
		"missing":  {Description: "d", Default: "~/nope"},
		"absolute": {Description: "d", Default: abs},
	}, nil)}
	res, err := Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.NewLock.ResolvedPlaceholders) != 0 {
		t.Fatalf("nothing may resolve: %v", res.NewLock.ResolvedPlaceholders)
	}
	if r := res.NewLock.UnresolvedPlaceholders["path:missing"]; !strings.Contains(r, "declared default ~/nope does not exist") {
		t.Errorf("reason = %q", r)
	}
	if _, ok := res.NewLock.UnresolvedPlaceholders["path:absolute"]; !ok {
		t.Errorf("an absolute default must not resolve: %v", res.NewLock.UnresolvedPlaceholders)
	}
}

// A declared remote picks the clone carrying it even when another clone
// shares the short name — which, looked up by short name, is an ambiguity.
func TestApply_DeclaredRemoteDisambiguatesShortName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	mine := filepath.Join(root, "a", "tools")
	theirs := filepath.Join(root, "b", "tools")
	fakeClone(t, mine, "git@gitlab.example.com:team/tools.git")
	fakeClone(t, theirs, "https://example.com/other/tools.git")

	m, kbRoot := placeholderKB(t)
	opts := clientApplyOptions(kbRoot, t.TempDir(), Lock{})
	opts.SearchRoots = []string{root}
	opts.Placeholders = map[string][]string{"repo:tools": {"kb"}}

	// Without a registry the short name is ambiguous.
	res, err := Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	if r := res.NewLock.UnresolvedPlaceholders["repo:tools"]; !strings.Contains(r, "ambiguous") {
		t.Fatalf("baseline: want an ambiguity, got resolved=%v unresolved=%v", res.NewLock.ResolvedPlaceholders, res.NewLock.UnresolvedPlaceholders)
	}

	opts.PathRegistries = map[string]PathRegistry{"kb": registryOf(nil, map[string]PathDecl{
		"tools": {Description: "the team's tools", Remote: "gitlab.example.com/team/tools"},
	})}
	res, err = Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.NewLock.ResolvedPlaceholders["repo:tools"]; got != mine {
		t.Errorf("resolved = %q, want %q (unresolved %v)", got, mine, res.NewLock.UnresolvedPlaceholders)
	}
}

// A repo default is used only when it is a live clone.
func TestApply_RepoDefaultMustBeAClone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m, kbRoot := placeholderKB(t)
	opts := clientApplyOptions(kbRoot, t.TempDir(), Lock{})
	opts.SearchRoots = []string{t.TempDir()}
	opts.Placeholders = map[string][]string{"repo:tools": {"kb"}}
	opts.PathRegistries = map[string]PathRegistry{"kb": registryOf(nil, map[string]PathDecl{"tools": {Description: "d", Default: "~/src/tools"}})}

	if err := os.MkdirAll(filepath.Join(home, "src", "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	if r := res.NewLock.UnresolvedPlaceholders["repo:tools"]; !strings.Contains(r, "not a git clone") {
		t.Fatalf("a plain directory must not satisfy a repo default: resolved=%v reason=%q", res.NewLock.ResolvedPlaceholders, r)
	}
	fakeClone(t, filepath.Join(home, "src", "tools"), "https://example.com/owner/tools.git")
	res, err = Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.NewLock.ResolvedPlaceholders["repo:tools"]; got != filepath.Join(home, "src", "tools") {
		t.Errorf("resolved = %q", got)
	}
}

// Two KBs declaring one key differently: the first in the provider's KB
// order wins, and one warning names both — never an error.
func TestApply_ConflictingDefaultsFirstKBWins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, d := range []string{"from-b", "from-a"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m, kbRoot := placeholderKB(t)
	opts := clientApplyOptions(kbRoot, t.TempDir(), Lock{})
	opts.Placeholders = map[string][]string{"path:shared": {"kb-a", "kb-b"}}
	opts.KBOrder = []string{"kb-b", "kb-a"}
	opts.PathRegistries = map[string]PathRegistry{
		"kb-a": registryOf(map[string]PathDecl{"shared": {Description: "a", Default: "~/from-a"}}, nil),
		"kb-b": registryOf(map[string]PathDecl{"shared": {Description: "b", Default: "~/from-b"}}, nil),
	}
	res, err := Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.NewLock.ResolvedPlaceholders["path:shared"]; got != filepath.Join(home, "from-b") {
		t.Errorf("resolved = %q, want kb-b's default (first in KB order)", got)
	}
	if len(res.RegistryWarnings) != 1 || !strings.Contains(res.RegistryWarnings[0], `"kb-a"`) || !strings.Contains(res.RegistryWarnings[0], `"kb-b"`) {
		t.Errorf("want one warning naming both KBs: %v", res.RegistryWarnings)
	}
}

// A declared key nothing cites joins the table when it resolves — an agent
// about to write learns it exists — and is dropped silently when it does not.
func TestApply_UncitedDeclaredKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o755); err != nil {
		t.Fatal(err)
	}
	m, kbRoot := placeholderKB(t)
	baseDir := t.TempDir()
	opts := clientApplyOptions(kbRoot, baseDir, Lock{})
	opts.PathRegistries = map[string]PathRegistry{"kb": registryOf(map[string]PathDecl{
		"ssh-dir":  {Description: "ssh", Default: "~/.ssh"},
		"no-where": {Description: "absent", Default: "~/absent"},
		"no-deflt": {Description: "no default"},
	}, nil)}
	res, err := Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.NewLock.ResolvedPlaceholders["path:ssh-dir"]; got != filepath.Join(home, ".ssh") {
		t.Errorf("uncited resolvable key: %v", res.NewLock.ResolvedPlaceholders)
	}
	if len(res.NewLock.UnresolvedPlaceholders) != 0 {
		t.Errorf("an uncited key must never be reported unresolved: %v", res.NewLock.UnresolvedPlaceholders)
	}
	if got := res.NewLock.PlaceholderSources["path:ssh-dir"]; !reflect.DeepEqual(got, []string{"kb"}) {
		t.Errorf("sources = %v, want the declaring KB", got)
	}
	if _, ok := res.NewLock.PlaceholderSources["path:no-where"]; ok {
		t.Errorf("a dropped key must not be recorded: %v", res.NewLock.PlaceholderSources)
	}
	if content := readInstructions(t, baseDir); !strings.Contains(content, "{{path:ssh-dir}}") {
		t.Errorf("table must list the uncited resolvable key:\n%s", content)
	}
}

func TestMergePathRegistries_Order(t *testing.T) {
	regs := map[string]PathRegistry{
		"a": registryOf(map[string]PathDecl{"k": {Description: "a", Default: "~/a"}, "same": {Description: "x", Default: "~/s"}}, nil),
		"b": registryOf(map[string]PathDecl{"k": {Description: "b", Default: "~/b"}, "same": {Description: "y", Default: "~/s"}}, nil),
		"c": registryOf(nil, map[string]PathDecl{"k": {Description: "c", Remote: "example.com/o/k"}}),
	}
	decls, by, warns := MergePathRegistries(nil, regs)
	if decls["path:k"].Default != "~/a" {
		t.Errorf("alphabetical fallback: %+v", decls["path:k"])
	}
	if decls["repo:k"].Remote != "example.com/o/k" {
		t.Errorf("kinds are separate keys: %+v", decls)
	}
	if !reflect.DeepEqual(by["path:same"], []string{"a", "b"}) {
		t.Errorf("declaredBy = %v", by)
	}
	// Only a differing default/remote is a conflict; a description is not.
	if len(warns) != 1 || !strings.Contains(warns[0], "path:k") {
		t.Errorf("warnings = %v", warns)
	}
	decls, _, _ = MergePathRegistries([]string{"b"}, regs)
	if decls["path:k"].Default != "~/b" {
		t.Errorf("explicit order: %+v", decls["path:k"])
	}
}

func TestRegistryDefaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for in, want := range map[string]string{
		"~":           home,
		"$HOME":       home,
		"~/.claude":   filepath.Join(home, ".claude"),
		"$HOME/.ssh":  filepath.Join(home, ".ssh"),
		"/Users/x":    "",
		"relative":    "",
		"~bob/x":      "",
		"~/../etc":    "",
		"$HOMEX/y":    "",
		"$HOME/a/../": "",
	} {
		got, ok := registryDefaultPath(in)
		if (want == "") == ok || (ok && filepath.Clean(got) != filepath.Clean(want)) {
			t.Errorf("registryDefaultPath(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}
