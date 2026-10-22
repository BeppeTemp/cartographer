package config

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/settingschema"
)

// The kb package cannot import config, so it keeps its own table of the keys
// a settings.yaml accepts. The schema is the source: this test fails when the
// two drift, in either direction, and when a key has no field to copy to.
func TestKBSettingsKeysMatchSchema(t *testing.T) {
	var kbKeys, serverKeys []string
	for _, k := range KBKeys {
		kbKeys = append(kbKeys, k.Path)
	}
	for _, k := range Schema.Keys {
		name, ok := strings.CutPrefix(k.Path, "kbs[].")
		if !ok {
			continue
		}
		if k.Scope == settingschema.ScopeKB {
			t.Errorf("%s is a KB key in the server schema: a KB's behaviour lives in its settings.yaml only (D906)", k.Path)
		}
		serverKeys = append(serverKeys, name)
	}
	sort.Strings(kbKeys)
	sort.Strings(serverKeys)
	if got := kb.SettingsKeyNames(); !reflect.DeepEqual(got, kbKeys) {
		t.Errorf("kb accepts %v, the schema's kb keys are %v", got, kbKeys)
	}
	if got := kb.SettingsServerOnlyNames(); !reflect.DeepEqual(got, serverKeys) {
		t.Errorf("kb refuses %v as server-only, the schema's server scope is %v", got, serverKeys)
	}
	for _, key := range kbKeys {
		if !setKBField(&KBSpec{}, KBSpec{}, key) {
			t.Errorf("setKBField has no case for %s", key)
		}
	}
}

func parse(t *testing.T, yml string) kb.SettingsState {
	t.Helper()
	s, bad, err := kb.ParseSettings([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	return kb.SettingsState{Present: true, Settings: s, Malformed: bad}
}

func TestResolveKBOnly(t *testing.T) {
	eff, info := ResolveKBSettings(KBSpec{Name: "kb-a", Remote: "https://git.example.com/user/kb-a.git"}, parse(t, "usage_stale_days: 30\n"))
	if eff.UsageStale() != 30 || eff.Name != "kb-a" || eff.Remote == "" {
		t.Fatalf("eff = %+v", eff)
	}
	if !info.FromKBHas("usage_stale_days") {
		t.Errorf("info = %+v", info)
	}
}

func TestResolveWithoutAFileIsTheDefaults(t *testing.T) {
	eff, info := ResolveKBSettings(KBSpec{Name: "kb-a"}, kb.SettingsState{})
	if eff.Name != "kb-a" || eff.UsageStale() != DefaultUsageStaleDays || info.Present || len(info.FromKB) != 0 {
		t.Fatalf("a KB without settings.yaml runs on the defaults: %+v %+v", eff, info)
	}
}

// branch is a key only a KB has (D903): it reaches the effective spec for the
// view, and a retired key of the same family does not.
func TestResolveBranchIsKBOnly(t *testing.T) {
	eff, info := ResolveKBSettings(KBSpec{}, parse(t, "branch: kb-data\ngit_branch: other\n"))
	if eff.Branch != "kb-data" || !info.FromKBHas("branch") {
		t.Errorf("eff = %+v info = %+v", eff, info)
	}
	if len(info.Findings) != 1 || !strings.Contains(info.Findings[0], "git_branch") {
		t.Errorf("findings = %v, want the retired git_branch named", info.Findings)
	}
}

// secrets and mcp_allowlist are gone from the KB file (D906): the file keeps
// reading, the keys are named in a finding and change nothing.
func TestResolveRetiredKBKeysAreFindings(t *testing.T) {
	_, info := ResolveKBSettings(KBSpec{}, parse(t, "secrets: true\nmcp_allowlist: [{name: a, transport: http, target: \"https://example.com/\"}]\nusage_stale_days: 30\n"))
	if len(info.Findings) != 2 || !strings.Contains(info.Findings[0], "secrets") || !strings.Contains(info.Findings[1], "mcp_allowlist") {
		t.Errorf("findings = %v", info.Findings)
	}
	if !info.FromKBHas("usage_stale_days") || info.FromKBHas("secrets") || info.FromKBHas("mcp_allowlist") {
		t.Errorf("from kb = %v", info.FromKB)
	}
}

func TestResolveUsageStaleDays(t *testing.T) {
	for _, tc := range []struct {
		yml      string
		want     int
		findings int
	}{
		{"", DefaultUsageStaleDays, 0},
		{"usage_stale_days: 30\n", 30, 0},
		{"usage_stale_days: 7\n", 7, 0},
		{"usage_stale_days: 3\n", 7, 1},  // raised to the minimum, with a warning
		{"usage_stale_days: 0\n", 42, 1}, // 0 no longer turns it off: the default, with a finding
		{"usage_stale_days: -2\n", 42, 1},
	} {
		eff, info := ResolveKBSettings(KBSpec{}, parse(t, tc.yml))
		if got := eff.UsageStale(); got != tc.want || len(info.Findings) != tc.findings {
			t.Errorf("%q: usage_stale_days = %d with findings %v, want %d and %d findings", tc.yml, got, info.Findings, tc.want, tc.findings)
		}
	}
}

func TestResolveUnparseableFileIsIgnoredWithAFinding(t *testing.T) {
	eff, info := ResolveKBSettings(KBSpec{Name: "kb-a"}, kb.SettingsState{Present: true, Unparseable: true,
		Malformed: []kb.SettingsMalformed{{Reason: "settings.yaml: not valid YAML"}}})
	if eff.Name != "kb-a" || len(info.Findings) != 1 {
		t.Errorf("eff=%+v info=%+v", eff, info)
	}
}

func TestSettingsViewShowsOriginKB(t *testing.T) {
	cfg := Default()
	cfg.KBs = []KBSpec{{Name: "kb-a", Path: "/data/kb-a"}}
	eff, info := ResolveKBSettings(cfg.KBs[0], parse(t, "usage_stale_days: 30\n"))
	cfg.RecordKBSettings("kb-a", eff, info.FromKB)
	cfg.RecordKBSettings("kb-discovered", KBSpec{UsageStaleDays: intp(7)}, []string{"usage_stale_days"})

	find := func(name, key string) Entry {
		for _, s := range cfg.Settings([]string{"kb-a", "kb-discovered"}).KBs {
			if s.Name != name {
				continue
			}
			for _, e := range s.Entries {
				if e.Key == key {
					return e
				}
			}
		}
		t.Fatalf("no %s/%s in the view", name, key)
		return Entry{}
	}
	if e := find("kb-a", "kbs[].usage_stale_days"); e.Origin != OriginKB {
		t.Errorf("usage_stale_days = %+v, want origin kb", e)
	}
	if e := find("kb-discovered", "kbs[].usage_stale_days"); e.Origin != OriginKB {
		t.Errorf("a discovered KB's settings.yaml must show origin kb: %+v", e)
	}
	// A refresh that drops a key takes its origin back.
	cfg.RecordKBSettings("kb-a", cfg.KBs[0], nil)
	if e := find("kb-a", "kbs[].usage_stale_days"); e.Origin != OriginDefault {
		t.Errorf("after the KB dropped the key: origin = %s, want default", e.Origin)
	}
}

func intp(n int) *int { return &n }

func TestKBSettingsSchemaIsTheKBKeys(t *testing.T) {
	s := KBSettingsSchema()
	if len(s.Keys) != len(kb.SettingsKeyNames()) {
		t.Fatalf("%d keys, kb accepts %d", len(s.Keys), len(kb.SettingsKeyNames()))
	}
	out, err := s.Format(nil)
	if err != nil {
		t.Fatal(err)
	}
	st := parse(t, string(out))
	if len(st.Settings.Keys()) != 0 || len(st.Malformed) != 0 {
		t.Errorf("the rendered file must set nothing: %v %v", st.Settings.Keys(), st.Malformed)
	}
	if !strings.Contains(string(out), "# usage_stale_days:") || strings.Contains(string(out), "ssh_key") {
		t.Errorf("rendered file:\n%s", out)
	}
}
