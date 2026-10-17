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
	for _, k := range KBOnlyKeys {
		kbKeys = append(kbKeys, k.Path)
	}
	for _, k := range Schema.Keys {
		name, ok := strings.CutPrefix(k.Path, "kbs[].")
		if !ok {
			continue
		}
		if k.Scope == settingschema.ScopeKB {
			kbKeys = append(kbKeys, name)
		} else {
			serverKeys = append(serverKeys, name)
		}
	}
	sort.Strings(kbKeys)
	sort.Strings(serverKeys)
	if got := kb.SettingsKeyNames(); !reflect.DeepEqual(got, kbKeys) {
		t.Errorf("kb accepts %v, the schema's kb scope is %v", got, kbKeys)
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

func always(string) bool { return true }

func TestResolveKBOnly(t *testing.T) {
	eff, info := ResolveKBSettings(KBSpec{}, nil, parse(t, "allow_artifact_write: true\nwrite_gate: error\n"))
	if !eff.AllowArtifactWrite || eff.WriteGateMode() != "error" {
		t.Fatalf("eff = %+v", eff)
	}
	if len(info.Ignored) != 0 || !info.FromKBHas("write_gate") {
		t.Errorf("info = %+v", info)
	}
}

func TestResolveServerOnly(t *testing.T) {
	srv := KBSpec{AllowArtifactWrite: true, WriteGate: "warning"}
	eff, info := ResolveKBSettings(srv, always, kb.SettingsState{})
	if !reflect.DeepEqual(eff, srv) || info.Present || len(info.FromKB) != 0 {
		t.Fatalf("a KB without settings.yaml must behave as before: %+v %+v", eff, info)
	}
}

// branch is a key only a KB has (D903): it reaches the effective spec for the
// view, and a retired key of the same family does not.
func TestResolveBranchIsKBOnly(t *testing.T) {
	eff, info := ResolveKBSettings(KBSpec{}, nil, parse(t, "branch: kb-data\ngit_branch: other\n"))
	if eff.Branch != "kb-data" || !info.FromKBHas("branch") {
		t.Errorf("eff = %+v info = %+v", eff, info)
	}
	if len(info.Findings) != 1 || !strings.Contains(info.Findings[0], "git_branch") {
		t.Errorf("findings = %v, want the retired git_branch named", info.Findings)
	}
}

func TestResolveBothKBWinsAndServerValueIsReported(t *testing.T) {
	srv := KBSpec{AllowArtifactWrite: true, WriteGate: "warning"}
	eff, info := ResolveKBSettings(srv, always, parse(t, "allow_artifact_write: false\nwrite_gate: warning\n"))
	if eff.AllowArtifactWrite {
		t.Error("the KB's false must beat the server's true")
	}
	if len(info.Ignored) != 1 || info.Ignored[0].Key != "allow_artifact_write" ||
		info.Ignored[0].ServerValue != "true" || info.Ignored[0].KBValue != "false" {
		t.Errorf("ignored = %+v (an equal write_gate is not ignored)", info.Ignored)
	}
}

func TestResolveServerExplicitFalseCountsAsSet(t *testing.T) {
	// A zero value written in kbs[] is absent from the marshalled spec: the
	// origin map is what says it was set (D743).
	_, info := ResolveKBSettings(KBSpec{}, always, parse(t, "allow_artifact_write: true\n"))
	if len(info.Ignored) != 1 {
		t.Errorf("ignored = %+v, want the explicit server false reported", info.Ignored)
	}
	_, info = ResolveKBSettings(KBSpec{}, func(string) bool { return false }, parse(t, "allow_artifact_write: true\n"))
	if len(info.Ignored) != 0 {
		t.Errorf("ignored = %+v, want none when the server did not set it", info.Ignored)
	}
}

func TestResolveDoesNotWriteThroughServerPointers(t *testing.T) {
	on := true
	budget := 3
	srv := KBSpec{AllowTemplateWrite: &on, DoctorBudget: &budget}
	eff, _ := ResolveKBSettings(srv, always, parse(t, "allow_template_write: false\ndoctor_budget: 8\n"))
	if !on || budget != 3 {
		t.Fatalf("the server spec was modified: on=%v budget=%d", on, budget)
	}
	if eff.TemplateWriteEnabled() || eff.DoctorBudgetItems() != 8 {
		t.Errorf("eff = %+v", eff)
	}
}

func TestResolveEmptyAutoRepairMeansNone(t *testing.T) {
	eff, _ := ResolveKBSettings(KBSpec{}, nil, parse(t, "auto_repair: []\n"))
	checks, isDefault := eff.AutoRepairChecks()
	if len(checks) != 0 || isDefault {
		t.Errorf("checks=%v default=%v, want an explicit none", checks, isDefault)
	}
}

func TestResolveLeavesOutValuesOnlyConfigCanCheck(t *testing.T) {
	eff, info := ResolveKBSettings(KBSpec{}, nil, parse(t, "auto_repair: [no_such_check]\nmcp_allowlist: [{name: a, transport: ftp, target: x}]\nsecrets: true\n"))
	if eff.AutoRepair != nil || eff.MCPAllowlist != nil {
		t.Errorf("invalid entries were applied: %+v", eff)
	}
	if len(info.Findings) != 2 || !eff.Secrets || !info.SecretsDeclared {
		t.Errorf("findings = %v secrets=%v", info.Findings, eff.Secrets)
	}
}

func TestResolveUnparseableFileIsIgnoredWithAFinding(t *testing.T) {
	srv := KBSpec{WriteGate: "error"}
	eff, info := ResolveKBSettings(srv, nil, kb.SettingsState{Present: true, Unparseable: true,
		Malformed: []kb.SettingsMalformed{{Reason: "settings.yaml: not valid YAML"}}})
	if eff.WriteGate != "error" || len(info.Findings) != 1 {
		t.Errorf("eff=%+v info=%+v", eff, info)
	}
}

func TestSettingsViewShowsOriginKB(t *testing.T) {
	cfg := Default()
	cfg.KBs = []KBSpec{{Name: "kb-a", Path: "/data/kb-a", WriteGate: "warning"}}
	cfg.mark(OriginFile, KBOriginKey("kb-a", "write_gate"))
	eff, info := ResolveKBSettings(cfg.KBs[0], cfg.ServerSetFunc("kb-a"), parse(t, "write_gate: error\ndoctor_budget: 4\n"))
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
	if e := find("kb-a", "kbs[].write_gate"); e.Origin != OriginKB || e.Value != "error" {
		t.Errorf("write_gate = %+v, want origin kb, value error", e)
	}
	if e := find("kb-a", "kbs[].doctor_budget"); e.Origin != OriginKB {
		t.Errorf("doctor_budget origin = %s", e.Origin)
	}
	if e := find("kb-discovered", "kbs[].usage_stale_days"); e.Origin != OriginKB {
		t.Errorf("a discovered KB's settings.yaml must show origin kb: %+v", e)
	}
	if e := find("kb-a", "kbs[].allow_artifact_write"); e.Origin != OriginDefault {
		t.Errorf("untouched key origin = %s", e.Origin)
	}
	// A refresh that drops a key takes its origin back.
	cfg.RecordKBSettings("kb-a", cfg.KBs[0], nil)
	if e := find("kb-a", "kbs[].write_gate"); e.Origin != OriginFile {
		t.Errorf("after the KB dropped the key: origin = %s, want file", e.Origin)
	}
}

func intp(n int) *int { return &n }

func TestKBSettingsSchemaIsTheKBScopeWithoutPrefix(t *testing.T) {
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
	if !strings.Contains(string(out), "# allow_artifact_write:") || strings.Contains(string(out), "ssh_key") {
		t.Errorf("rendered file:\n%s", out)
	}
}
