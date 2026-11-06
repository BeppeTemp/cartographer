package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/execbit"
)

func testStore(t *testing.T) Store { return Store{Root: filepath.Join(t.TempDir(), "plugins")} }

func goodDigest(t *testing.T) string {
	t.Helper()
	p, probs := ReadTree(goodTree(), testOpts)
	if len(probs) != 0 {
		t.Fatalf("problems: %v", probs)
	}
	return p.Digest
}

func putGood(t *testing.T, s Store) string {
	t.Helper()
	if !execbit.Supported {
		t.Skip("the good tree has an executable hook; a host without the bit refuses it")
	}
	d := goodDigest(t)
	if err := s.Put("team-forks", goodTree(), d, testOpts); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestStoreRoundTrip(t *testing.T) {
	s := testStore(t)
	d := putGood(t, s)
	p, err := s.Open("team-forks", d, testOpts)
	if err != nil {
		t.Fatal(err)
	}
	if p.Digest != d || len(p.Files) != 6 {
		t.Errorf("read back %s with %d files", p.Digest, len(p.Files))
	}
	// Ignored entries are not stored.
	if _, err := os.Stat(filepath.Join(s.Root, "team-forks", d, "README.md")); err == nil {
		t.Error("README.md was stored")
	}
	// Putting it again keeps the directory.
	fi, _ := os.Stat(filepath.Join(s.Root, "team-forks", d))
	if err := s.Put("team-forks", goodTree(), d, testOpts); err != nil {
		t.Fatal(err)
	}
	fi2, _ := os.Stat(filepath.Join(s.Root, "team-forks", d))
	if !os.SameFile(fi, fi2) {
		t.Error("an existing digest directory was replaced")
	}
}

func TestStorePutRefusals(t *testing.T) {
	s := testStore(t)
	d := goodDigest(t)
	if err := s.Put("team-forks", goodTree(), strings.Repeat("0", 64), testOpts); err == nil {
		t.Error("a wrong digest was stored")
	}
	if err := s.Put("other-name", goodTree(), d, testOpts); err == nil {
		t.Error("a tree whose manifest names another plugin was stored")
	}
	if err := s.Put("team-forks", []TreeFile{tf("x", "y")}, d, testOpts); !errors.Is(err, ErrInvalid) {
		t.Errorf("an invalid tree: %v", err)
	}
	if entries, _ := os.ReadDir(s.Root); len(entries) != 0 {
		for _, e := range entries {
			if e.Name() != "" {
				t.Errorf("left behind: %s", e.Name())
			}
		}
	}
}

func TestStoreOpenErrors(t *testing.T) {
	s := testStore(t)
	d := goodDigest(t)
	if _, err := s.Open("team-forks", d, testOpts); !errors.Is(err, ErrNotFetched) {
		t.Errorf("empty store: %v", err)
	}
	putGood(t, s)
	skill := filepath.Join(s.Root, "team-forks", d, "skills", "helper", "notes.md")
	if err := os.WriteFile(skill, []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open("team-forks", d, testOpts); !errors.Is(err, ErrModified) {
		t.Errorf("edited byte: %v", err)
	}
	// A repair: Put replaces a corrupt directory of the same digest.
	if err := s.Put("team-forks", goodTree(), d, testOpts); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open("team-forks", d, testOpts); err != nil {
		t.Errorf("after repair: %v", err)
	}
	// Same bytes, a running version the manifest no longer accepts: invalid.
	old := testOpts
	old.RunningVersion = "0.0.1"
	if _, err := s.Open("team-forks", d, old); !errors.Is(err, ErrInvalid) {
		t.Errorf("unsatisfied range: %v", err)
	}
}

func TestStoreRefusesBadNames(t *testing.T) {
	s := testStore(t)
	d := goodDigest(t)
	for _, n := range []string{"../x", "/abs", "a/b", "", "X", ".tmp-x", "..", "a\\b"} {
		if _, err := s.Open(n, d, testOpts); err == nil || errors.Is(err, ErrNotFetched) {
			t.Errorf("Open(%q): %v", n, err)
		}
		if err := s.Put(n, goodTree(), d, testOpts); err == nil {
			t.Errorf("Put(%q) succeeded", n)
		}
	}
	for _, dg := range []string{"", "../..", strings.Repeat("A", 64), strings.Repeat("a", 63)} {
		if _, err := s.Open("team-forks", dg, testOpts); err == nil || errors.Is(err, ErrNotFetched) {
			t.Errorf("Open digest %q: %v", dg, err)
		}
	}
	if _, err := os.Stat(filepath.Dir(s.Root) + "/x"); err == nil {
		t.Error("a path escaped the root")
	}
}

func TestStoreSweepsInterruptedWrites(t *testing.T) {
	s := testStore(t)
	putGood(t, s)
	old := filepath.Join(s.Root, ".tmp-team-forks-old")
	fresh := filepath.Join(s.Root, ".tmp-team-forks-fresh")
	for _, p := range []string{old, fresh} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	if err := s.GC(map[string]Keep{"team-forks": {}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("an old temp directory was kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a recent temp directory was swept: it may be a write in progress")
	}
}

func TestStoreGC(t *testing.T) {
	s := testStore(t)
	d := putGood(t, s)
	other := strings.Repeat("b", 64)
	third := strings.Repeat("c", 64)
	for _, p := range []string{
		filepath.Join(s.Root, "team-forks", other),
		filepath.Join(s.Root, "team-forks", third),
		filepath.Join(s.Root, "gone-plugin", other),
	} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.GC(map[string]Keep{"team-forks": {Installed: other, Approved: d}}); err != nil {
		t.Fatal(err)
	}
	exists := func(p ...string) bool {
		_, err := os.Stat(filepath.Join(append([]string{s.Root}, p...)...))
		return err == nil
	}
	if !exists("team-forks", d) || !exists("team-forks", other) {
		t.Error("GC removed the installed or the approved digest")
	}
	if exists("team-forks", third) {
		t.Error("GC kept a digest nobody references")
	}
	if exists("gone-plugin") {
		t.Error("GC kept a plugin that is not configured")
	}
	if err := (Store{Root: filepath.Join(t.TempDir(), "none")}).GC(nil); err != nil {
		t.Errorf("GC on a missing store: %v", err)
	}
}

func TestStoreRefusesLinkedDirectory(t *testing.T) {
	if _, err := os.Lstat(os.TempDir()); err != nil {
		t.Skip()
	}
	s := testStore(t)
	d := goodDigest(t)
	target := t.TempDir()
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(s.Root, "team-forks")); err != nil {
		t.Skip("no symlinks here")
	}
	if err := s.Put("team-forks", goodTree(), d, testOpts); err == nil && execbit.Supported {
		t.Error("Put followed a linked plugin directory")
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Errorf("something was written through the link: %v", entries)
	}
}

func TestStateTable(t *testing.T) {
	s := testStore(t)
	d := putGood(t, s)
	e := clientconfig.PluginEntry{Source: "/x", Installed: d, Approved: d, Enabled: true}
	cases := []struct {
		name string
		mut  func(*clientconfig.PluginEntry)
		opts Options
		want Status
	}{
		{"active", func(*clientconfig.PluginEntry) {}, testOpts, StatusActive},
		{"disabled", func(e *clientconfig.PluginEntry) { e.Enabled = false }, testOpts, StatusDisabled},
		{"disabled wins over pending", func(e *clientconfig.PluginEntry) { e.Enabled = false; e.Approved = "" }, testOpts, StatusDisabled},
		{"pending", func(e *clientconfig.PluginEntry) { e.Approved = "" }, testOpts, StatusPending},
		{"not fetched", func(e *clientconfig.PluginEntry) { e.Approved = strings.Repeat("d", 64) }, testOpts, StatusNotFetched},
		{"newer installed is ignored", func(e *clientconfig.PluginEntry) { e.Installed = strings.Repeat("d", 64) }, testOpts, StatusActive},
		{"invalid", func(*clientconfig.PluginEntry) {}, Options{RunningVersion: "0.0.1"}, StatusInvalid},
	}
	for _, c := range cases {
		entry := e
		c.mut(&entry)
		got, p := State("team-forks", entry, s, c.opts)
		if got != c.want || (got == StatusActive) != (p != nil) {
			t.Errorf("%s: %q, plugin %v; want %q", c.name, got, p != nil, c.want)
		}
	}
	notes := filepath.Join(s.Root, "team-forks", d, "skills", "helper", "notes.md")
	if err := os.WriteFile(notes, []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, p := State("team-forks", e, s, testOpts); got != StatusModified || p != nil {
		t.Errorf("edited: %q", got)
	}
}

// The config package cannot import this one, so it repeats the name rule.
func TestConfigNameRuleMatchesManifest(t *testing.T) {
	for _, n := range []string{"ab", "a", "team-forks", "Team", "a_b", "1ab", "../x", strings.Repeat("a", 41), strings.Repeat("a", 42), "kb", "bundle", "claude"} {
		var c clientconfig.Config
		err := c.SetPlugin(n, clientconfig.PluginEntry{Source: "/x"})
		if (err == nil) != nameRe.MatchString(n) {
			t.Errorf("%q: config accepts=%v, manifest rule=%v", n, err == nil, nameRe.MatchString(n))
		}
	}
}
