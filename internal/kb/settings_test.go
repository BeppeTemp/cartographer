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
usage_stale_days: 3
branch: kb-data
`)
	if len(bad) != 0 {
		t.Fatalf("malformed = %v", bad)
	}
	if got := len(s.Keys()); got != 2 {
		t.Errorf("accepted %d keys, want 2: %v", got, s.Keys())
	}
	if v, ok := s.String("branch"); !ok || v != "kb-data" {
		t.Errorf("String(branch) = %q, %v", v, ok)
	}
	if !s.Has("usage_stale_days") || s.Has("write_gate") {
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
branch: kb-data
frobnicate: 1
ssh_key: /home/user/.ssh/id
remote: git@example.com:user/kb-a.git
usage_stale_days: -1
mcp_allowlist: [{name: a, transport: http, target: "https://example.com/mcp"}]
secrets: true
git_forge: github
git_branch: kb-data
write_gate: error
auto_repair: [duplicate_link]
repair_on_write: false
doctor_auto_interval: 0
doctor_interval: 7d
doctor_mode: assisted
doctor_budget: 4
`)
	if !s.Has("branch") || len(s.Keys()) != 1 {
		t.Errorf("accepted keys = %v, want only branch", s.Keys())
	}
	want := map[string]string{
		"frobnicate":           "unknown setting",
		"ssh_key":              "<kb>.ssh",
		"remote":               "server-only",
		"usage_stale_days":     "7 or more",
		"mcp_allowlist":        "always distributed",
		"secrets":              "under secrets/",
		"git_forge":            "removed setting",
		"git_branch":           "use branch",
		"write_gate":           "always on",
		"auto_repair":          "every safe fix is always applied",
		"repair_on_write":      "every safe fix is always applied",
		"doctor_auto_interval": "every safe fix is always applied",
		"doctor_interval":      "14 days",
		"doctor_mode":          "every safe fix is always applied",
		"doctor_budget":        "budget is a parameter of the session",
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
	os.WriteFile(path, []byte("usage_stale_days: 30\nbogus: 1\n"), 0o644)
	st, err = k.ReadSettings()
	if err != nil || !st.Present || !st.Settings.Has("usage_stale_days") || len(st.Malformed) != 1 {
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
	os.WriteFile(filepath.Join(k.Root, SettingsFile), []byte("usage_stale_days: 30\n"), 0o644)
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
	os.WriteFile(filepath.Join(root, SettingsFile), []byte("usage_stale_days: 30\n"), 0o644)
	if _, err := InitWithSettings(root, "user", "user@example.com", []byte("# other\n")); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(root, SettingsFile))
	if string(b) != "usage_stale_days: 30\n" {
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

// A KB that still carries the server git profile (D903) is told what to do, and
// the profile itself, a value that cannot be honoured, marks the finding as a
// refusal. The other removed keys are only ignored.
func TestParseSettingsRetiredKeys(t *testing.T) {
	t.Parallel()
	s, bad := keysOf(t, `
git_profile: server
git_base_branch: main
github_owner: user
git_branch: kb-data
usage_stale_days: 30
`)
	if len(s.Keys()) != 1 || !s.Has("usage_stale_days") {
		t.Errorf("accepted keys = %v, want only usage_stale_days", s.Keys())
	}
	refused := map[string]bool{}
	reasons := map[string]string{}
	for _, m := range bad {
		refused[m.Key] = m.Refuse
		reasons[m.Key] = m.Reason
	}
	if !refused["git_profile"] {
		t.Error("git_profile: server must be a refusal")
	}
	for _, k := range []string{"git_base_branch", "github_owner", "git_branch"} {
		if refused[k] {
			t.Errorf("%s is only ignored, not refused", k)
		}
	}
	if !strings.Contains(reasons["git_profile"], "unprotect the branch") || !strings.Contains(reasons["git_profile"], "`branch`") {
		t.Errorf("git_profile reason does not say what to do: %q", reasons["git_profile"])
	}
	if !strings.Contains(reasons["git_branch"], "use branch") {
		t.Errorf("git_branch reason does not name its replacement: %q", reasons["git_branch"])
	}

	// git_profile: local was the default: removed, ignored, not refused.
	_, bad = keysOf(t, "git_profile: local\n")
	if len(bad) != 1 || bad[0].Refuse {
		t.Errorf("git_profile: local = %v, want one finding, not a refusal", bad)
	}

	// The author keys are gone everywhere (D904): a KB that names one is told
	// who the author is now, not that the key belongs on the server.
	_, bad = keysOf(t, "author_name: Bot\n")
	if len(bad) != 1 || bad[0].Refuse || !strings.Contains(bad[0].Reason, AuthorSettingRemoved) {
		t.Errorf("author_name = %v, want one finding that names the writer, not a refusal", bad)
	}

	// Artifact writes are always allowed (D906): the two settings that gated
	// them are removed, and a file that sets either is told so.
	for _, key := range []string{"allow_artifact_write", "allow_template_write"} {
		s, bad = keysOf(t, key+": true\n")
		if len(s.Keys()) != 0 || len(bad) != 1 || bad[0].Key != key || bad[0].Refuse || !strings.Contains(bad[0].Reason, ArtifactWriteAlwaysOn) {
			t.Errorf("%s = keys %v findings %v, want one finding that says artifact writes are always allowed, not a refusal", key, s.Keys(), bad)
		}
	}
}
