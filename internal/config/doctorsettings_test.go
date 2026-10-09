package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDoctorSettings(t *testing.T) {
	dir := t.TempDir()
	for name, tc := range map[string]struct {
		body     string
		wantErr  string
		wantDays int
	}{
		"default":   {body: "kbs:\n  - path: /tmp/kb\n", wantDays: DefaultDoctorIntervalDays},
		"suffix":    {body: "kbs:\n  - path: /tmp/kb\n    doctor_interval: 7d\n", wantDays: 7},
		"bare":      {body: "kbs:\n  - path: /tmp/kb\n    doctor_interval: \"30\"\n", wantDays: 30},
		"disabled":  {body: "kbs:\n  - path: /tmp/kb\n    doctor_interval: \"0\"\n", wantDays: 0},
		"negative":  {body: "kbs:\n  - path: /tmp/kb\n    doctor_interval: -3d\n", wantErr: "doctor_interval"},
		"duration":  {body: "kbs:\n  - path: /tmp/kb\n    doctor_interval: 2w\n", wantErr: "doctor_interval"},
		"checks":    {body: "kbs:\n  - path: /tmp/kb\n    auto_repair: [nonstandard_field, duplicate_link]\n", wantDays: DefaultDoctorIntervalDays},
		"unknown":   {body: "kbs:\n  - path: /tmp/kb\n    auto_repair: [nonstandard_field, stale_open]\n", wantErr: `"stale_open"`},
		"no-all":    {body: "kbs:\n  - path: /tmp/kb\n    auto_repair: [all]\n", wantErr: `no "all"`},
		"judgement": {body: "kbs:\n  - path: /tmp/kb\n    auto_repair: [map_misfit]\n", wantErr: `"map_misfit"`},
	} {
		path := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: err = %v, want it to contain %s", name, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if days, _ := cfg.KBs[0].DoctorIntervalDays(); days != tc.wantDays {
			t.Errorf("%s: days = %d, want %d", name, days, tc.wantDays)
		}
	}
}

// An absent auto_repair means the default list, an explicit empty one means
// none, and a list means exactly that list (D323). The nil-versus-empty
// distinction is what the whole feature rests on, so it is pinned through the
// real YAML loader rather than by building a KBSpec in Go.
func TestLoadAutoRepairDefaultVersusExplicit(t *testing.T) {
	dir := t.TempDir()
	for name, tc := range map[string]struct {
		body        string
		want        []string
		wantDefault bool
	}{
		"default_auto":   {body: "kbs:\n  - path: /tmp/kb\n", want: DefaultAutoRepair, wantDefault: true},
		"explicit_empty": {body: "kbs:\n  - path: /tmp/kb\n    auto_repair: []\n", want: []string{}},
		"explicit_list":  {body: "kbs:\n  - path: /tmp/kb\n    auto_repair: [duplicate_link]\n", want: []string{"duplicate_link"}},
	} {
		path := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, isDefault := cfg.KBs[0].AutoRepairChecks()
		if isDefault != tc.wantDefault || strings.Join(got, ",") != strings.Join(tc.want, ",") || (got == nil) != (tc.want == nil) {
			t.Errorf("%s: AutoRepairChecks() = %v, %v; want %v, %v", name, got, isDefault, tc.want, tc.wantDefault)
		}
	}
	// A discovered KB has the zero spec: it is maintained like any other.
	if got, isDefault := (KBSpec{}).AutoRepairChecks(); !isDefault || len(got) != len(DefaultAutoRepair) {
		t.Errorf("zero spec: %v, %v", got, isDefault)
	}
	if err := ValidateAutoRepair(DefaultAutoRepair); err != nil {
		t.Errorf("the default list must be made of fixable checks: %v", err)
	}
}

func TestLoadDoctorAutoInterval(t *testing.T) {
	dir := t.TempDir()
	for name, tc := range map[string]struct {
		body     string
		wantErr  string
		wantDays int
	}{
		"default":  {body: "kbs:\n  - path: /tmp/kb\n", wantDays: DefaultDoctorAutoIntervalDays},
		"suffix":   {body: "kbs:\n  - path: /tmp/kb\n    doctor_auto_interval: 7d\n", wantDays: 7},
		"disabled": {body: "kbs:\n  - path: /tmp/kb\n    doctor_auto_interval: \"0\"\n", wantDays: 0},
		"bad":      {body: "kbs:\n  - path: /tmp/kb\n    doctor_auto_interval: 2w\n", wantErr: "doctor_auto_interval"},
	} {
		path := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: err = %v, want it to contain %s", name, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if days, _ := cfg.KBs[0].DoctorAutoIntervalDays(); days != tc.wantDays {
			t.Errorf("%s: days = %d, want %d", name, days, tc.wantDays)
		}
	}
}

// repair_on_write absent follows auto_repair (D349); an explicit false is the
// opt-out. Pinned through the YAML loader, as AutoRepairChecks is.
func TestLoadRepairOnWrite(t *testing.T) {
	dir := t.TempDir()
	for name, tc := range map[string]struct {
		body string
		want bool
	}{
		"absent":          {"kbs:\n  - path: /tmp/kb\n", true},
		"empty_auto":      {"kbs:\n  - path: /tmp/kb\n    auto_repair: []\n", false},
		"explicit_false":  {"kbs:\n  - path: /tmp/kb\n    repair_on_write: false\n", false},
		"true_empty_auto": {"kbs:\n  - path: /tmp/kb\n    auto_repair: []\n    repair_on_write: true\n", true},
		"explicit_list":   {"kbs:\n  - path: /tmp/kb\n    auto_repair: [duplicate_link]\n", true},
	} {
		path := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := cfg.KBs[0].RepairOnWriteEnabled(); got != tc.want {
			t.Errorf("%s: RepairOnWriteEnabled() = %v, want %v", name, got, tc.want)
		}
	}
}
