package clientconfig_test

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// WP4: a comment a person wrote survives a save that changes something else.
func TestSavePreservesUserCommentsAndUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "# my machine\nserver_url: http://127.0.0.1:39273/mcp\nserver_name: cartographer\nauth: false\ntoken_env: CARTOGRAPHER_TOKENS\nagents:\n  - claude\n# I bind by hand\nclients:\n  claude:\n    kbs:\n      - alpha\ntrust: true\nmy_extension: 1 # note\nsearch_roots:\n  - ~/Documents\n")
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Bind("claude", "beta"); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, clientconfig.Path(dir))
	for _, want := range []string{"# my machine", "# I bind by hand\nclients:", "my_extension: 1 # note", "- beta"} {
		if !strings.Contains(raw, want) {
			t.Errorf("lost %q:\n%s", want, raw)
		}
	}
	if strings.Contains(raw, "# Which KBs each connected provider receives") {
		t.Errorf("documented a key the user had commented:\n%s", raw)
	}
	again, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if kbs, _ := again.BoundKBs("claude"); strings.Join(kbs, ",") != "alpha,beta" {
		t.Errorf("binding = %v", kbs)
	}
}

// A key the user had as a commented-out line, now set by Save, is not listed twice.
func TestSaveRemovesNewlySetKeyFromTheCommentedBlock(t *testing.T) {
	dir := t.TempDir()
	cfg := clientconfig.Default()
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, clientconfig.Path(dir))
	if !strings.Contains(raw, "# search_roots:") {
		t.Fatalf("an unset key should be listed commented out:\n%s", raw)
	}
	cfg.SearchRoots = []string{"~/work"}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	raw = readFile(t, clientconfig.Path(dir))
	if strings.Contains(raw, "# search_roots:") {
		t.Errorf("search_roots is set and still listed as available:\n%s", raw)
	}
	if !hasKey(raw, "search_roots") {
		t.Errorf("search_roots not written:\n%s", raw)
	}
	if strings.Count(raw, "# --- available settings ---") != 1 {
		t.Errorf("want one block:\n%s", raw)
	}
}

// A file written by the previous version (no comments, caches inside) gains the
// comments on the next save, loses the caches to the state file, and loads to
// the same Config.
func TestUpgradeFromUncommentedFileWithLegacyCaches(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "server_url: http://127.0.0.1:39273/mcp\nserver_name: cartographer\nauth: false\ntoken_env: CARTOGRAPHER_TOKENS\nagents:\n- claude\nknown_kbs:\n- alpha\nserver_mount_mode: routed\nserver_routed_path: /mcp\ntrust: true\nsearch_roots:\n- ~/Documents\n")
	before, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if before.ServerMountMode != "routed" || len(before.KnownKBs) != 1 {
		t.Fatalf("legacy caches not read: %+v", before)
	}
	if err := clientconfig.Save(dir, before); err != nil {
		t.Fatal(err)
	}
	before.Warnings = nil // the legacy keys it warned of are gone now
	raw := readFile(t, clientconfig.Path(dir))
	for _, gone := range []string{"known_kbs", "server_mount_mode", "server_routed_path"} {
		if hasKey(raw, gone) {
			t.Errorf("%s still in the user file:\n%s", gone, raw)
		}
	}
	if !strings.Contains(raw, "# Default:") {
		t.Errorf("the file gained no comments:\n%s", raw)
	}
	after, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("Config changed across the upgrade:\n%+v\n%+v", before, after)
	}
	if _, err := os.Stat(clientconfig.StatePath(dir)); err != nil {
		t.Errorf("state file not written: %v", err)
	}
}

// WP3: a sync that only refreshes the caches writes the state file and leaves
// .cartographer.yaml alone.
func TestCacheOnlySaveLeavesTheUserFileUntouched(t *testing.T) {
	dir := t.TempDir()
	cfg := clientconfig.Default()
	cfg.Agents = []string{"claude"}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(clientconfig.Path(dir), old, old); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, clientconfig.Path(dir))

	loaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded.KnownKBs = []string{"alpha", "beta"}
	loaded.ServerMountMode = "routed"
	loaded.ServerRoutedPath = "/mcp"
	if err := clientconfig.Save(dir, loaded); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(clientconfig.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().Equal(old) || readFile(t, clientconfig.Path(dir)) != before {
		t.Errorf("a cache-only save rewrote .cartographer.yaml (mtime %v, want %v)", st.ModTime(), old)
	}
	again, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(again.KnownKBs, ",") != "alpha,beta" || again.ServerMountMode != "routed" || again.ServerRoutedPath != "/mcp" {
		t.Errorf("caches not persisted: %+v", again)
	}
}

// A corrupt state file is a cache that went bad: Load ignores it and falls back.
func TestCorruptStateFileIsTreatedAsAbsent(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "server_url: http://127.0.0.1:39273/mcp\nknown_kbs:\n- legacy\n")
	if err := os.WriteFile(clientconfig.StatePath(dir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load must survive a corrupt state file: %v", err)
	}
	if strings.Join(cfg.KnownKBs, ",") != "legacy" {
		t.Errorf("KnownKBs = %v, want the legacy fallback", cfg.KnownKBs)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if s := readFile(t, clientconfig.StatePath(dir)); !strings.Contains(s, "legacy") {
		t.Errorf("the next save did not rebuild the state file:\n%s", s)
	}
}

// Dropping a removed key does not take a person's comment with it.
func TestRemovedKeyKeepsItsUserComment(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "server_url: http://127.0.0.1:39273/mcp\n# keep me\nsearch_depth: 6\ntrust: true\n")
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if raw := readFile(t, clientconfig.Path(dir)); !strings.Contains(raw, "# keep me") || hasKey(raw, "search_depth") {
		t.Errorf("want the comment kept and the key gone:\n%s", raw)
	}
}
