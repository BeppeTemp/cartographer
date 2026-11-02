package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const authConfig = "auth:\n  mode: token\n  tokens:\n    - name: a\n      token: secret\nkbs:\n  - remote: https://git.example.com/user/wiki.git\n"

// A failure on the last step leaves the old bytes: the file holds auth.tokens,
// and a truncated one would turn authentication off at the next start (#1501).
func TestWriteFileAtomic_FailedRenameKeepsOldBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(path, []byte(authConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	orig := renameFile
	renameFile = func(string, string) error { return errors.New("disk full") }
	t.Cleanup(func() { renameFile = orig })

	if err := DropKBAliasesKey(path); err != nil { // no kb_aliases key: nothing to write
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("auth: {}\n")); err == nil {
		t.Fatal("the rename failure was swallowed")
	}
	got, _ := os.ReadFile(path)
	if string(got) != authConfig {
		t.Errorf("file changed: %q", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("a temporary file was left behind: %v", entries)
	}
}

func TestDropKBAliasesKey_FailedWriteKeepsOldBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yaml")
	body := authConfig + "kb_aliases:\n  - old: a\n    new: b\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	orig := renameFile
	renameFile = func(string, string) error { return errors.New("disk full") }
	t.Cleanup(func() { renameFile = orig })
	if err := DropKBAliasesKey(path); err == nil {
		t.Fatal("the failed write was not reported")
	}
	if got, _ := os.ReadFile(path); string(got) != body {
		t.Errorf("file changed: %q", got)
	}
}

// An edit keeps the operator's two-space sequences and the file's mode.
func TestWriteYAML_KeepsLayoutAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(path, []byte(authConfig+"kb_aliases:\n  - old: a\n    new: b\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := DropKBAliasesKey(path); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != authConfig {
		t.Errorf("rewritten layout differs:\n%s", got)
	}
	if strings.Contains(string(got), "    - remote") {
		t.Errorf("sequence was re-indented to 4 spaces:\n%s", got)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", info.Mode().Perm())
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
}
