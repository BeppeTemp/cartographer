package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/settingschema"
	"gopkg.in/yaml.v3"
)

// An old server file: removed keys of two kinds (a top-level group and a list
// entry), a value that switches a feature off, and a value a person chose.
const oldServerYAML = `# my server
http: ":9090" # kept as written
git:
  autocommit: false
  sync: false
  in_window: 5s
  ssh_key: /etc/kb/id
web:
  enabled: false
kbs:
  - remote: https://git.example.com/user/kb-a.git
    git_branch: team/kb-data
    write_gate: error
  - path: /srv/kb-b
`

// webOff stands for the "switches a feature off" rule the later plans add: it is
// a registry entry, so the machinery is exercised with the real schema.
var withWebOff = func() settingschema.Registry {
	r := Retired
	r.Off = append(append([]settingschema.Off(nil), r.Off...), settingschema.Off{Path: "web.enabled", Value: "false", Plan: 903})
	return r
}()

func TestRealignServerFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(oldServerYAML), 0o640); err != nil {
		t.Fatal(err)
	}

	rep, backup, err := Schema.RealignFile(path, withWebOff, "0.25.0")
	if err != nil {
		t.Fatal(err)
	}
	// The report lists the three kinds of change.
	got := strings.Join(rep.Lines(), "\n")
	for _, want := range []string{
		"removed git.autocommit:",
		"removed git.sync:",
		"removed git.in_window:",
		"removed kbs[0].git_branch: replaced by branch in the KB's settings.yaml",
		"removed kbs[1].path:",
		"reset web.enabled: false switched a feature off",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
	// The original is kept, byte for byte, beside the file.
	if backup != path+".bak-0.25.0" {
		t.Fatalf("backup = %q", backup)
	}
	if orig, err := os.ReadFile(backup); err != nil || string(orig) != oldServerYAML {
		t.Errorf("backup does not hold the original: %v\n%s", err, orig)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o640 {
		t.Errorf("the realigned file lost its permissions: %v %v", st, err)
	}

	out, _ := os.ReadFile(path)
	text := string(out)
	// Removed keys are dropped, the off value is reset, what a person chose stays.
	var doc struct {
		Git map[string]any   `yaml:"git"`
		Web map[string]any   `yaml:"web"`
		KBs []map[string]any `yaml:"kbs"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Git) != 1 || doc.Git["ssh_key"] == nil {
		t.Errorf("git = %v, want only ssh_key", doc.Git)
	}
	if doc.Web != nil {
		t.Errorf("web = %v, want the reset mapping gone", doc.Web)
	}
	if len(doc.KBs) != 1 || len(doc.KBs[0]) != 2 {
		t.Errorf("kbs = %v, want the one entry with remote and write_gate", doc.KBs)
	}
	for _, kept := range []string{"# my server", `http: ":9090" # kept as written`, "ssh_key: /etc/kb/id", "write_gate: error", "remote: https://git.example.com/user/kb-a.git"} {
		if !strings.Contains(text, kept) {
			t.Errorf("%q was lost:\n%s", kept, text)
		}
	}
	if !strings.Contains(text, "# SSH private key used to reach remote KBs.") || !strings.Contains(text, "# --- available settings ---") {
		t.Errorf("the schema comments were not written:\n%s", text)
	}

	// The result loads with nothing left to warn about, and means what it meant.
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("the realigned file does not load: %v", err)
	}
	if len(cfg.Warnings) != 0 || cfg.HTTP != ":9090" || !cfg.Web.Enabled || cfg.KBs[0].WriteGate != "error" {
		t.Errorf("realigned config = %+v warnings=%v", cfg, cfg.Warnings)
	}
	if cfg.Git.SSHKey != "/etc/kb/id" {
		t.Errorf("git.ssh_key = %q", cfg.Git.SSHKey)
	}
}

// A second run changes nothing: the same bytes, an empty report, no new backup.
func TestRealignServerFileSecondRunIsANoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(oldServerYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Schema.RealignFile(path, withWebOff, "0.25.0"); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	st1, _ := os.Stat(path)

	rep, backup, err := Schema.RealignFile(path, withWebOff, "0.25.0")
	if err != nil || !rep.Empty() || backup != "" {
		t.Fatalf("second run = report %v backup %q err %v, want nothing", rep.Lines(), backup, err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Errorf("the second run rewrote the file:\n%s\n---\n%s", first, second)
	}
	if st2, _ := os.Stat(path); !st2.ModTime().Equal(st1.ModTime()) {
		t.Error("the second run touched the file")
	}
	if matches, _ := filepath.Glob(path + ".bak-*"); len(matches) != 1 {
		t.Errorf("backups = %v, want the one from the first run", matches)
	}
}

// A backup under the same version is never overwritten: the first one is the
// file as it was before the upgrade.
func TestRealignServerFileKeepsTheOldestBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("git:\n  sync: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Schema.RealignFile(path, Retired, "0.25.0"); err != nil {
		t.Fatal(err)
	}
	// A removed key edited back in by hand, then realigned again.
	if err := os.WriteFile(path, []byte("git:\n  autocommit: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, second, err := Schema.RealignFile(path, Retired, "0.25.0")
	if err != nil || second != path+".bak-0.25.0.1" {
		t.Fatalf("second backup = %q, %v", second, err)
	}
	if orig, _ := os.ReadFile(path + ".bak-0.25.0"); string(orig) != "git:\n  sync: false\n" {
		t.Errorf("the first backup was overwritten: %q", orig)
	}
}

// A file that cannot be written (a read-only mount) stays as it was and keeps
// working; no half-written backup is left behind.
func TestRealignServerFileUnwritableLeavesTheOriginal(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permission bits and a non-root user")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(oldServerYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	rep, _, err := Schema.RealignFile(path, Retired, "0.25.0")
	if err == nil {
		t.Fatal("RealignFile on a read-only directory returned no error")
	}
	if !rep.Substantive() {
		t.Errorf("the report of a file that holds removed keys is not substantive: %v", rep.Lines())
	}
	if got, _ := os.ReadFile(path); string(got) != oldServerYAML {
		t.Errorf("the original changed:\n%s", got)
	}
	if matches, _ := filepath.Glob(path + ".bak-*"); len(matches) != 0 {
		t.Errorf("a backup was left behind: %v", matches)
	}
	if _, err := Load(path); err != nil {
		t.Errorf("the unrealigned file no longer loads: %v", err)
	}
}

func TestRealignServerFileAbsentIsFine(t *testing.T) {
	rep, backup, err := RealignServerFile(filepath.Join(t.TempDir(), "nope.yaml"), "0.25.0")
	if err != nil || !rep.Empty() || backup != "" {
		t.Fatalf("absent file: %v %q %v", rep.Lines(), backup, err)
	}
}

// A removed key edited back in by hand after the realignment is a warning, not
// an error, and the value changes nothing.
func TestRemovedKeyAddedBackByHandWarns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("git:\n  author_name: Bot\n  author_email: bot@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RealignServerFile(path, "0.25.0"); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("kbs:\n  - remote: https://git.example.com/user/kb-a.git\n    git_branch: team/kb-data\n")
	f.Close()
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("a removed key is not an error: %v", err)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "kbs[0].git_branch") || !strings.Contains(cfg.Warnings[0], "branch in the KB's settings.yaml") {
		t.Errorf("warnings = %q, want the one that names the replacement", cfg.Warnings)
	}
	if cfg.RefusalError() != nil {
		t.Errorf("git_branch is ignored, not refused: %v", cfg.RefusalError())
	}
}

// The same machinery over a KB's settings.yaml, in bytes: the server commits
// the result itself.
func TestRealignKBSettings(t *testing.T) {
	in := "# policy\nwrite_gate: error\ngit_profile: local\ngit_branch: team/kb-data\ngithub_owner: acme\nbranch: kb-data\n"
	out, rep, err := RealignKBSettings([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, gone := range []string{"git_profile: local", "git_branch:", "github_owner: acme"} {
		if strings.Contains(text, gone) {
			t.Errorf("%q survived:\n%s", gone, text)
		}
	}
	for _, kept := range []string{"# policy", "write_gate: error", "branch: kb-data"} {
		if !strings.Contains(text, kept) {
			t.Errorf("%q was lost:\n%s", kept, text)
		}
	}
	got := strings.Join(rep.Lines(), "\n")
	for _, want := range []string{"removed git_profile:", "removed git_branch: replaced by branch", "removed github_owner:"} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
	again, rep2, err := RealignKBSettings(out)
	if err != nil || string(again) != text || !rep2.Empty() {
		t.Errorf("second run: %v %v", err, rep2.Lines())
	}
	// A KB created by `kb create` is already in this form.
	fresh, _ := KBSettingsSchema().Format(nil)
	if _, rep3, err := RealignKBSettings(fresh); err != nil || !rep3.Empty() {
		t.Errorf("a fresh settings.yaml is not canonical: %v %v", err, rep3.Lines())
	}
}
