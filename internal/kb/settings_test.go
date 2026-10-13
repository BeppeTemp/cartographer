package kb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func keysOf(t *testing.T, yml string) (Settings, []SettingsMalformed) {
	t.Helper()
	s, bad, err := ParseSettings([]byte(yml))
	if err != nil {
		t.Fatalf("ParseSettings: %v", err)
	}
	return s, bad
}

func TestParseSettingsValid(t *testing.T) {
	t.Parallel()
	s, bad := keysOf(t, `
allow_artifact_write: true
write_gate: error
auto_repair: [duplicate_link]
doctor_interval: 7d
doctor_budget: 5
usage_stale_days: 0
mcp_allowlist:
  - {name: tool-a, transport: http, target: "https://example.com/mcp"}
secrets: true
git_profile: server
`)
	if len(bad) != 0 {
		t.Fatalf("malformed = %v", bad)
	}
	if got := len(s.Keys()); got != 9 {
		t.Errorf("accepted %d keys, want 9: %v", got, s.Keys())
	}
	if !s.Has("secrets") || s.Has("doctor_mode") {
		t.Errorf("Has is wrong: %v", s.Keys())
	}
}

func TestParseSettingsEmptyAndNull(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "# only comments\n", "~\n"} {
		s, bad, err := ParseSettings([]byte(in))
		if err != nil || len(bad) != 0 || len(s.Keys()) != 0 {
			t.Errorf("%q: keys=%v bad=%v err=%v", in, s.Keys(), bad, err)
		}
	}
}

func TestParseSettingsWholeFileIgnored(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"key: [unclosed", "- a\n- b\n"} {
		if _, _, err := ParseSettings([]byte(in)); err == nil {
			t.Errorf("%q: want an error (whole file ignored)", in)
		}
	}
}

func TestParseSettingsKeyErrorsLeaveTheRestAlone(t *testing.T) {
	t.Parallel()
	s, bad := keysOf(t, `
allow_artifact_write: true
frobnicate: 1
ssh_key: /home/user/.ssh/id
write_gate: loud
doctor_interval: soon
doctor_budget: 0
usage_stale_days: -1
auto_repair: not-a-list
mcp_allowlist: [{name: a, port: 1}]
repair_on_write: maybe
git_forge: gitlab
write_gate: off
`)
	if !s.Has("allow_artifact_write") || len(s.Keys()) != 1 {
		t.Errorf("accepted keys = %v, want only allow_artifact_write", s.Keys())
	}
	want := map[string]string{
		"frobnicate":       "unknown setting",
		"ssh_key":          "server-only",
		"write_gate":       "", // first one invalid; the second is a duplicate
		"doctor_interval":  "number of days",
		"doctor_budget":    "1 or more",
		"usage_stale_days": "0 or more",
		"auto_repair":      "list of names",
		"mcp_allowlist":    "not name, transport or target",
		"repair_on_write":  "true or false",
		"git_forge":        "one of github",
	}
	got := map[string]string{}
	for _, m := range bad {
		if _, dup := got[m.Key]; !dup {
			got[m.Key] = m.Reason
		}
	}
	for key, frag := range want {
		r, ok := got[key]
		if !ok || !strings.Contains(r, frag) {
			t.Errorf("%s: reason %q, want it to contain %q", key, r, frag)
		}
	}
}

func TestReadSettings(t *testing.T) {
	t.Parallel()
	k := &KB{Root: t.TempDir()}
	st, err := k.ReadSettings()
	if err != nil || st.Present {
		t.Fatalf("absent: %+v %v", st, err)
	}
	path := filepath.Join(k.Root, SettingsFile)
	os.WriteFile(path, []byte("write_gate: warning\nbogus: 1\n"), 0o644)
	st, err = k.ReadSettings()
	if err != nil || !st.Present || !st.Settings.Has("write_gate") || len(st.Malformed) != 1 {
		t.Fatalf("present: %+v %v", st, err)
	}
	os.WriteFile(path, []byte("[x"), 0o644)
	st, _ = k.ReadSettings()
	if !st.Unparseable || len(st.Malformed) != 1 || len(st.Settings.Keys()) != 0 {
		t.Fatalf("unparseable: %+v", st)
	}
	os.Remove(path)
	os.Symlink(filepath.Join(k.Root, "elsewhere"), path)
	if _, err := k.ReadSettings(); err == nil {
		t.Error("a symlinked settings.yaml must be refused (D148)")
	}
}

func TestRefreshSettingsReloadsOnlyOnChange(t *testing.T) {
	t.Parallel()
	k := &KB{Root: t.TempDir()}
	calls := 0
	k.SettingsReload = func(*KB) { calls++ }
	k.MarkSettingsSeen()
	k.RefreshSettings()
	if calls != 0 {
		t.Fatalf("unchanged file reloaded %d times", calls)
	}
	os.WriteFile(filepath.Join(k.Root, SettingsFile), []byte("write_gate: error\n"), 0o644)
	k.RefreshSettings()
	k.RefreshSettings()
	if calls != 1 {
		t.Fatalf("reload ran %d times after one change, want 1", calls)
	}
	os.Remove(filepath.Join(k.Root, SettingsFile))
	k.RefreshSettings()
	if calls != 2 {
		t.Fatalf("removing the file must reload too, calls = %d", calls)
	}
}

func TestInitWithSettingsWritesTheFileOnlyForANewKB(t *testing.T) {
	root := filepath.Join(t.TempDir(), "kb-a")
	if _, err := InitWithSettings(root, "user", "user@example.com", []byte("# commented\n")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, SettingsFile))
	if err != nil || string(b) != "# commented\n" {
		t.Fatalf("settings.yaml = %q, %v", b, err)
	}
	// An existing KB gets nothing written, and its own file is not replaced.
	os.WriteFile(filepath.Join(root, SettingsFile), []byte("write_gate: error\n"), 0o644)
	if _, err := InitWithSettings(root, "user", "user@example.com", []byte("# other\n")); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(root, SettingsFile))
	if string(b) != "write_gate: error\n" {
		t.Errorf("an existing settings.yaml was rewritten: %q", b)
	}
	other := filepath.Join(t.TempDir(), "kb-b")
	if _, err := Init(other); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(other, SettingsFile)); err == nil {
		t.Error("Init without settings must not write the file")
	}
}
