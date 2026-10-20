package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
)

func runRename(t *testing.T, old, newName, dataDir, cfgPath string) (int, string) {
	t.Helper()
	var code int
	out := withStdout(t, func() {
		withNoGuidance(t, func() {
			code = cmdKBRename([]string{old, newName, "--data", dataDir, "--config", cfgPath})
		})
	})
	return code, out
}

// loadAliases is what a server restarted on cfgPath resolves (D906): the
// config loads and its data directory's state file holds the list.
func loadAliases(t *testing.T, cfgPath string) []config.KBAlias {
	t.Helper()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("the rewritten config does not load: %v", err)
	}
	got, err := cfg.KBAliases()
	if err != nil {
		t.Fatalf("the state file does not read: %v", err)
	}
	return got
}

func wantAliases(t *testing.T, got []config.KBAlias, want ...config.KBAlias) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("kb_aliases = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kb_aliases = %v, want %v", got, want)
		}
	}
}

// A KB with a kbs[] entry records its alias in the same write, and the
// operator's comments and fields survive.
func TestCmdKBRename_RecordsAliasWithEntry(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	withClientServerURL(t, "")
	dataDir := seedRenameKB(t, "wiki")
	cfgPath := writeServerConfig(t, "# operator note\ndata: "+dataDir+"\nkbs:\n  - remote: https://git.example.com/user/wiki.git\n    tool_prefix: wk\n")

	if code, out := runRename(t, "wiki", "atlas", dataDir, cfgPath); code != 0 {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	wantAliases(t, loadAliases(t, cfgPath), config.KBAlias{From: "wiki", To: "atlas"})
	if _, err := os.Stat(filepath.Join(dataDir, ".cartographer", "state.json")); err != nil {
		t.Errorf("kb rename did not write the state file: %v", err)
	}
	body, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(body), "kb_aliases") {
		t.Errorf("the alias went into the server YAML:\n%s", body)
	}
	for _, want := range []string{"# operator note", "tool_prefix: wk", "name: atlas"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("config lost %q:\n%s", want, body)
		}
	}
}

// A KB mounted by discovery has no entry: the alias is still recorded and no
// entry is created (it would change what governs the KB, D151).
func TestCmdKBRename_RecordsAliasWithoutEntry(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	withClientServerURL(t, "")
	dataDir := seedRenameKB(t, "wiki")
	cfgPath := writeServerConfig(t, "data: "+dataDir+"\n")

	if code, out := runRename(t, "wiki", "atlas", dataDir, cfgPath); code != 0 {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	wantAliases(t, loadAliases(t, cfgPath), config.KBAlias{From: "wiki", To: "atlas"})
	cfg, _ := config.Load(cfgPath)
	if len(cfg.KBs) != 0 {
		t.Errorf("a kbs[] entry was created: %v", cfg.KBs)
	}
	body, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(body), "kb_aliases") {
		t.Errorf("the alias went into the server YAML:\n%s", body)
	}
}

// Renaming twice keeps the list flat and single hop: a → b → c ends as
// a: c and b: c; renaming back drops the alias of the restored name.
func TestCmdKBRename_KeepsAliasesFlat(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	withClientServerURL(t, "")
	dataDir := seedRenameKB(t, "kb-a")
	cfgPath := writeServerConfig(t, "data: "+dataDir+"\n")

	for _, step := range [][2]string{{"kb-a", "kb-b"}, {"kb-b", "kb-c"}} {
		if code, out := runRename(t, step[0], step[1], dataDir, cfgPath); code != 0 {
			t.Fatalf("rename %v: exit = %d:\n%s", step, code, out)
		}
	}
	wantAliases(t, loadAliases(t, cfgPath),
		config.KBAlias{From: "kb-a", To: "kb-c"}, config.KBAlias{From: "kb-b", To: "kb-c"})

	if code, out := runRename(t, "kb-c", "kb-a", dataDir, cfgPath); code != 0 {
		t.Fatalf("rename back: exit = %d:\n%s", code, out)
	}
	wantAliases(t, loadAliases(t, cfgPath),
		config.KBAlias{From: "kb-b", To: "kb-a"}, config.KBAlias{From: "kb-c", To: "kb-a"})
}

// Without a server config the alias is still recorded: the state file lives
// under the data dir, not in the config.
func TestCmdKBRename_NoConfigStillRecordsTheAlias(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	withClientServerURL(t, "")
	dataDir := seedRenameKB(t, "wiki")

	code, out := runRename(t, "wiki", "atlas", dataDir, filepath.Join(dataDir, "no-config.yaml"))
	if code != 0 {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	st, err := config.ReadServerState(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	wantAliases(t, st.KBAliases, config.KBAlias{From: "wiki", To: "atlas"})
}

// A list the server YAML still carries is moved into the state file by the
// command that edits it, and the key leaves the YAML, so it cannot come back.
func TestCmdKBRename_MovesLegacyAliasesIntoTheStateFile(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	withClientServerURL(t, "")
	dataDir := seedRenameKB(t, "wiki")
	cfgPath := writeServerConfig(t, "data: "+dataDir+"\nkb_aliases:\n  - from: kb-old\n    to: wiki\n")

	if code, out := runRename(t, "wiki", "atlas", dataDir, cfgPath); code != 0 {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	body, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(body), "kb_aliases") {
		t.Errorf("the legacy list stayed in the YAML:\n%s", body)
	}
	wantAliases(t, loadAliases(t, cfgPath),
		config.KBAlias{From: "kb-old", To: "atlas"}, config.KBAlias{From: "wiki", To: "atlas"})
}

// A config write that fails leaves no alias and the directory where it was.
func TestApplyKBRename_FailedWriteLeavesNoAlias(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the file mode this test relies on")
	}
	dataDir := seedRenameKB(t, "wiki")
	cfgPath := writeServerConfig(t, "data: "+dataDir+"\nkbs:\n  - remote: https://git.example.com/user/wiki.git\n")
	if err := os.Chmod(cfgPath, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(cfgPath, 0o600) })

	p := &renamePlan{
		Old: "wiki", New: "atlas",
		OldPath: filepath.Join(dataDir, "wiki"), NewPath: filepath.Join(dataDir, "atlas"),
		DataDir: dataDir, ConfigPath: cfgPath, EntryIndex: 0,
	}
	if err := applyKBRename(p); err == nil {
		t.Fatal("an unwritable config should fail the rename")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "wiki", "data", "index.md")); err != nil {
		t.Errorf("the directory was not renamed back: %v", err)
	}
	if st, _ := config.ReadServerState(dataDir); len(st.KBAliases) != 0 {
		t.Errorf("an alias outlived the failed rename: %v", st.KBAliases)
	}
}

// Unmounting the target of a rename removes the aliases that point at it, in
// the write the backup covers, and leaves the others.
func TestCmdKBUnmount_PrunesAliases(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	writeClientBindings(t, "http://127.0.0.1:39273")
	dataDir, _ := seedPushedKB(t, "kb-a")
	cfgPath := writeServerConfig(t, "# operator note\ndata: "+dataDir+"\n")
	seedAliases(t, dataDir, config.KBAlias{From: "kb-old", To: "kb-a"}, config.KBAlias{From: "kb-x", To: "kb-y"})

	code, out := runUnmount(t, "kb-a", "--data", dataDir, "--config", cfgPath)
	if code != 0 {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "the alias kb-old → kb-a") {
		t.Errorf("the preflight does not list the alias:\n%s", out)
	}
	wantAliases(t, loadAliases(t, cfgPath), config.KBAlias{From: "kb-x", To: "kb-y"})
	body, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(body), "# operator note") {
		t.Errorf("the operator's comment was dropped:\n%s", body)
	}
}

// A list still in the server YAML is settled into the state file first, so an
// unmount prunes it too and the YAML key does not bring the alias back.
func TestCmdKBUnmount_PrunesLegacyAliases(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	writeClientBindings(t, "http://127.0.0.1:39273")
	dataDir, _ := seedPushedKB(t, "kb-a")
	cfgPath := writeServerConfig(t, "data: "+dataDir+"\nkb_aliases:\n  - from: kb-old\n    to: kb-a\n")

	if code, out := runUnmount(t, "kb-a", "--data", dataDir, "--config", cfgPath); code != 0 {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	body, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(body), "kb_aliases") {
		t.Errorf("the legacy key was left:\n%s", body)
	}
	wantAliases(t, loadAliases(t, cfgPath))
}

// An item that starts at the unmounted name goes too: it was dormant while a
// KB was mounted under the name (a mounted name wins), and would take effect
// again the moment that KB went away.
func TestCmdKBUnmount_PrunesAliasStartingAtName(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	writeClientBindings(t, "http://127.0.0.1:39273")
	dataDir, _ := seedPushedKB(t, "kb-a")
	cfgPath := writeServerConfig(t, "data: "+dataDir+"\n")
	seedAliases(t, dataDir, config.KBAlias{From: "kb-a", To: "kb-b"}, config.KBAlias{From: "kb-x", To: "kb-y"})

	code, out := runUnmount(t, "kb-a", "--data", dataDir, "--config", cfgPath)
	if code != 0 {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "the alias kb-a → kb-b") {
		t.Errorf("the preflight does not list the alias:\n%s", out)
	}
	wantAliases(t, loadAliases(t, cfgPath), config.KBAlias{From: "kb-x", To: "kb-y"})
}

// seedAliases writes the server state file the way `kb rename` would have.
func seedAliases(t *testing.T, dataDir string, aliases ...config.KBAlias) {
	t.Helper()
	if err := config.WriteServerState(dataDir, config.ServerState{KBAliases: aliases}); err != nil {
		t.Fatal(err)
	}
}
