package configurator_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// A client's config file, or a directory above it, may be a link (a dotfile
// manager, a monorepo checkout). Writing through it lands in the link's target,
// so Apply and Remove refuse it for every provider and leave the target alone
// (D148).

func symlinkProviders(t *testing.T) []configurator.Provider {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	return configurator.ProviderList()
}

func symlinkCfg() *configurator.ServerConfig {
	return &configurator.ServerConfig{Name: "wiki", URL: "https://mcp.example.test/mcp"}
}

// seededConfig is the bytes of provider's config file after a connect, written
// in a scratch home: a target that holds our entry, so Remove has work to do.
func seededConfig(t *testing.T, p configurator.Provider) (rel string, data []byte) {
	t.Helper()
	home := t.TempDir()
	r, err := configurator.Emit(symlinkCfg(), p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configurator.Apply([]*configurator.EmitResult{r}, home, false); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(home, r.FilePath))
	if err != nil {
		t.Fatal(err)
	}
	return r.FilePath, data
}

// snapshot lists every entry under dir with its bytes, so "unchanged" covers
// files that must not appear as well as bytes that must not change.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() {
			out[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameSnapshot(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	var keys []string
	for k := range before {
		keys = append(keys, k)
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		b, bok := before[k]
		a, aok := after[k]
		if bok != aok || a != b {
			t.Errorf("%s: %q changed through the link (before %q, after %q)", what, k, b, a)
		}
	}
}

func TestApplyAndRemove_RefuseASymlinkedConfigFile(t *testing.T) {
	for _, p := range symlinkProviders(t) {
		t.Run(string(p), func(t *testing.T) {
			rel, seeded := seededConfig(t, p)
			home := t.TempDir()
			elsewhere := t.TempDir()
			target := filepath.Join(elsewhere, "target")
			if err := os.WriteFile(target, seeded, 0o644); err != nil {
				t.Fatal(err)
			}
			full := filepath.Join(home, rel)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, full); err != nil {
				t.Fatal(err)
			}
			before := snapshot(t, elsewhere)

			r, err := configurator.Emit(&configurator.ServerConfig{Name: "other", URL: "https://mcp.example.test/other"}, p)
			if err != nil {
				t.Fatal(err)
			}
			for _, dry := range []bool{false, true} {
				written, err := configurator.Apply([]*configurator.EmitResult{r}, home, dry)
				if !errors.Is(err, safewrite.ErrSymlinkDestination) {
					t.Fatalf("Apply(dry=%v) = %v, want ErrSymlinkDestination", dry, err)
				}
				if len(written) != 0 {
					t.Errorf("Apply(dry=%v) reported %v written", dry, written)
				}
				if !strings.Contains(err.Error(), full) {
					t.Errorf("error %q does not name %s", err, full)
				}
				if _, err := configurator.Remove(symlinkCfg(), p, home, dry); !errors.Is(err, safewrite.ErrSymlinkDestination) {
					t.Fatalf("Remove(dry=%v) = %v, want ErrSymlinkDestination", dry, err)
				}
			}
			sameSnapshot(t, "config file", before, snapshot(t, elsewhere))
			if fi, err := os.Lstat(full); err != nil || fi.Mode()&os.ModeSymlink == 0 {
				t.Errorf("the link itself was replaced: %v %v", fi, err)
			}
		})
	}
}

func TestApplyAndRemove_RefuseASymlinkedParentDirectory(t *testing.T) {
	for _, p := range symlinkProviders(t) {
		t.Run(string(p), func(t *testing.T) {
			rel, seeded := seededConfig(t, p)
			parts := strings.Split(filepath.ToSlash(rel), "/")
			if len(parts) < 2 {
				t.Skipf("%s keeps its config at the base dir, with no directory above it", p)
			}
			// Link each directory component in turn, so a link at any depth is covered.
			for depth := 1; depth < len(parts); depth++ {
				home := t.TempDir()
				elsewhere := t.TempDir()
				// The link target already holds the rest of the path, with our entry in it.
				if err := os.MkdirAll(filepath.Join(elsewhere, filepath.Join(parts[depth:len(parts)-1]...)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(elsewhere, filepath.Join(parts[depth:]...)), seeded, 0o644); err != nil {
					t.Fatal(err)
				}
				linkAt := filepath.Join(append([]string{home}, parts[:depth]...)...)
				if err := os.MkdirAll(filepath.Dir(linkAt), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(elsewhere, linkAt); err != nil {
					t.Fatal(err)
				}
				before := snapshot(t, elsewhere)

				r, err := configurator.Emit(&configurator.ServerConfig{Name: "other", URL: "https://mcp.example.test/other"}, p)
				if err != nil {
					t.Fatal(err)
				}
				for _, dry := range []bool{false, true} {
					if _, err := configurator.Apply([]*configurator.EmitResult{r}, home, dry); !errors.Is(err, safewrite.ErrSymlinkDestination) {
						t.Fatalf("depth %d: Apply(dry=%v) = %v, want ErrSymlinkDestination", depth, dry, err)
					}
					if _, err := configurator.Remove(symlinkCfg(), p, home, dry); !errors.Is(err, safewrite.ErrSymlinkDestination) {
						t.Fatalf("depth %d: Remove(dry=%v) = %v, want ErrSymlinkDestination", depth, dry, err)
					}
				}
				sameSnapshot(t, "parent directory", before, snapshot(t, elsewhere))
			}
		})
	}
}

// One refused result must not stop the others of the same call.
func TestApply_ARefusalDoesNotAbortTheOtherResults(t *testing.T) {
	symlinkProviders(t)
	home := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(home, ".codex")); err != nil {
		t.Fatal(err)
	}
	var results []*configurator.EmitResult
	for _, p := range []configurator.Provider{configurator.ProviderCodex, configurator.ProviderClaudeCode} {
		r, err := configurator.Emit(symlinkCfg(), p)
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, r)
	}
	written, err := configurator.Apply(results, home, false)
	if !errors.Is(err, safewrite.ErrSymlinkDestination) {
		t.Fatalf("Apply = %v, want ErrSymlinkDestination", err)
	}
	if len(written) != 1 || filepath.Base(written[0]) != ".claude.json" {
		t.Errorf("written = %v, want only .claude.json", written)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("the link target gained %d entries", len(entries))
	}
}

// The legacy .codex/config.json cleanup writes too.
func TestRemove_RefusesASymlinkedLegacyCodexJSON(t *testing.T) {
	symlinkProviders(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	target := filepath.Join(elsewhere, "legacy.json")
	const legacy = `{"mcpServers":{"wiki":{"url":"https://mcp.example.test/mcp"}},"other":1}`
	if err := os.WriteFile(target, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".codex", "config.json")); err != nil {
		t.Fatal(err)
	}
	for _, dry := range []bool{false, true} {
		if _, err := configurator.Remove(symlinkCfg(), configurator.ProviderCodex, home, dry); !errors.Is(err, safewrite.ErrSymlinkDestination) {
			t.Fatalf("Remove(dry=%v) = %v, want ErrSymlinkDestination", dry, err)
		}
	}
	if b, _ := os.ReadFile(target); string(b) != legacy {
		t.Errorf("legacy target changed: %q", b)
	}
}

// The Codex helpers rewrite config.toml in place: a link at the file is refused.
func TestCodexTableHelpers_RefuseASymlinkedFile(t *testing.T) {
	symlinkProviders(t)
	dir := t.TempDir()
	const begin = "# cartographer:x:begin — x"
	const end = "# cartographer:x:end"
	content := begin + "\n[mcp_servers.x]\nurl = \"u\"\n[notice]\nk = 1\n" + end + "\n[mcp_servers.y]\nurl = \"u\"\n"
	target := filepath.Join(dir, "target.toml")
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := configurator.EvictForeignTablesFromBlock(link, begin, end, "[mcp_servers.x]\nurl = \"u\"\n"); !errors.Is(err, safewrite.ErrSymlinkDestination) {
		t.Errorf("EvictForeignTablesFromBlock = %v, want ErrSymlinkDestination", err)
	}
	owned := func(key []string, _ string) bool { return len(key) == 2 && key[1] == "y" }
	if _, err := configurator.AdoptCodexOrphanTables(link, owned); !errors.Is(err, safewrite.ErrSymlinkDestination) {
		t.Errorf("AdoptCodexOrphanTables = %v, want ErrSymlinkDestination", err)
	}
	if b, _ := os.ReadFile(target); string(b) != content {
		t.Errorf("target changed: %q", b)
	}
}

// A refusal needs something to write or remove: a link over a config with
// nothing of ours in it is a no-op, never an error.
func TestRemove_NothingOfOursNeverRefuses(t *testing.T) {
	for _, p := range symlinkProviders(t) {
		t.Run(string(p), func(t *testing.T) {
			rel, _ := seededConfig(t, p)
			// A config that holds a different server, not ours.
			other := &configurator.ServerConfig{Name: "other", URL: "https://mcp.example.test/other"}
			r, err := configurator.Emit(other, p)
			if err != nil {
				t.Fatal(err)
			}
			scratch := t.TempDir()
			if filepath.Ext(rel) == ".toml" {
				// The block is shared by every entry: "nothing of ours" is a
				// file without it.
				if err := os.MkdirAll(filepath.Dir(filepath.Join(scratch, rel)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(scratch, rel), []byte("key = 1\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			} else if _, err := configurator.Apply([]*configurator.EmitResult{r}, scratch, false); err != nil {
				t.Fatal(err)
			}
			parts := strings.Split(filepath.ToSlash(rel), "/")
			home := t.TempDir()
			if len(parts) == 1 {
				if err := os.Symlink(filepath.Join(scratch, rel), filepath.Join(home, rel)); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(filepath.Join(scratch, parts[0]), filepath.Join(home, parts[0])); err != nil {
				t.Fatal(err)
			}
			before := snapshot(t, scratch)
			for _, dry := range []bool{false, true} {
				removed, err := configurator.Remove(symlinkCfg(), p, home, dry)
				if err != nil || removed {
					t.Errorf("Remove(dry=%v) = %v, %v; want false, nil", dry, removed, err)
				}
			}
			sameSnapshot(t, "no-op remove", before, snapshot(t, scratch))
		})
	}
}

// Both files or neither: a refusal on the legacy config.json must not leave the
// managed block already removed from config.toml.
func TestRemove_CodexLegacyRefusalLeavesBothFilesUntouched(t *testing.T) {
	symlinkProviders(t)
	home := t.TempDir()
	r, err := configurator.Emit(symlinkCfg(), configurator.ProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configurator.Apply([]*configurator.EmitResult{r}, home, false); err != nil {
		t.Fatal(err)
	}
	toml := filepath.Join(home, r.FilePath)
	tomlBefore, _ := os.ReadFile(toml)

	elsewhere := t.TempDir()
	legacy := filepath.Join(elsewhere, "legacy.json")
	const legacyBody = `{"mcpServers":{"wiki":{"url":"https://mcp.example.test/mcp"}},"other":1}`
	if err := os.WriteFile(legacy, []byte(legacyBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(legacy, filepath.Join(home, ".codex", "config.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := configurator.Remove(symlinkCfg(), configurator.ProviderCodex, home, false); !errors.Is(err, safewrite.ErrSymlinkDestination) {
		t.Fatalf("Remove = %v, want ErrSymlinkDestination", err)
	}
	if b, _ := os.ReadFile(toml); string(b) != string(tomlBefore) {
		t.Errorf("config.toml changed although the legacy file was refused:\n%s", b)
	}
	if b, _ := os.ReadFile(legacy); string(b) != legacyBody {
		t.Errorf("legacy target changed: %s", b)
	}
}

// The adopting and evicting rewrites check the directories above the file too,
// but only when they have something to rewrite.
func TestCodexTableHelpersUnder_RefuseALinkedDirectoryOnlyWhenRewriting(t *testing.T) {
	symlinkProviders(t)
	const begin = "# cartographer:x:begin — x"
	const end = "# cartographer:x:end"
	owned := func(key []string, _ string) bool { return len(key) == 2 && key[1] == "y" }

	elsewhere := t.TempDir()
	content := begin + "\n[mcp_servers.x]\nurl = \"u\"\n[notice]\nk = 1\n" + end + "\n[mcp_servers.y]\nurl = \"u\"\n"
	if err := os.WriteFile(filepath.Join(elsewhere, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(base, ".codex")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, ".codex", "config.toml")
	before := snapshot(t, elsewhere)

	if _, err := configurator.AdoptCodexOrphanTablesUnder(base, path, owned); !errors.Is(err, safewrite.ErrSymlinkDestination) {
		t.Errorf("AdoptCodexOrphanTablesUnder = %v, want ErrSymlinkDestination", err)
	}
	if _, err := configurator.EvictTablesFromBlockUnder(base, path, begin, end, func(key []string) bool { return false }); !errors.Is(err, safewrite.ErrSymlinkDestination) {
		t.Errorf("EvictTablesFromBlockUnder = %v, want ErrSymlinkDestination", err)
	}
	// Nothing to adopt or evict: no rewrite, so no refusal.
	if got, err := configurator.AdoptCodexOrphanTablesUnder(base, path, func([]string, string) bool { return false }); err != nil || got != nil {
		t.Errorf("AdoptCodexOrphanTablesUnder with nothing to adopt = %v, %v; want nil, nil", got, err)
	}
	if got, err := configurator.EvictTablesFromBlockUnder(base, path, begin, end, func(key []string) bool { return true }); err != nil || got != nil {
		t.Errorf("EvictTablesFromBlockUnder with nothing foreign = %v, %v; want nil, nil", got, err)
	}
	sameSnapshot(t, "codex tables", before, snapshot(t, elsewhere))
}

// #1172 review: a refused global OpenCode config leaves the legacy
// $HOME/opencode.json untouched and reports nothing removed.
func TestRemove_OpenCodeRefusalOnTheGlobalFileKeepsTheLegacyEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	home := t.TempDir()
	const legacyBody = `{"mcp":{"wiki":{"type":"remote"}}}`
	legacy := filepath.Join(home, "opencode.json")
	if err := os.WriteFile(legacy, []byte(legacyBody), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(target, []byte(`{"mcp":{"wiki":{"type":"remote"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(home, filepath.FromSlash(configurator.OpenCodeConfigPath))
	if err := os.MkdirAll(filepath.Dir(global), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, global); err != nil {
		t.Fatal(err)
	}
	removed, err := configurator.Remove(&configurator.ServerConfig{Name: "wiki"}, configurator.ProviderOpenCode, home, false)
	if !errors.Is(err, safewrite.ErrSymlinkDestination) {
		t.Fatalf("Remove err = %v, want ErrSymlinkDestination", err)
	}
	if removed {
		t.Error("removed = true on a refusal")
	}
	if got, _ := os.ReadFile(legacy); string(got) != legacyBody {
		t.Errorf("legacy opencode.json changed: %s", got)
	}
}
