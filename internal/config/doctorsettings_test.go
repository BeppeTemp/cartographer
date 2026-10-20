package config

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// resolveFile is the effective KB policy of a settings.yaml body, and the
// findings that left entries out (D906: the KB file is the only place a KB's
// policy is set).
func resolveFile(t *testing.T, body string) (KBSpec, []string) {
	t.Helper()
	s, bad, err := kb.ParseSettings([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	eff, info := ResolveKBSettings(KBSpec{}, kb.SettingsState{Present: true, Settings: s, Malformed: bad})
	return eff, info.Findings
}

func TestResolveDoctorSettings(t *testing.T) {
	for name, tc := range map[string]struct {
		body     string
		wantErr  string
		wantDays int
	}{
		"default":  {body: "", wantDays: DefaultDoctorIntervalDays},
		"assisted": {body: "doctor_mode: assisted\n", wantDays: DefaultDoctorIntervalDays},
		// Accepted and ignored since D704: a 0.23 file keeps loading.
		"unattended": {body: "doctor_mode: unattended\n", wantDays: DefaultDoctorIntervalDays},
		"assisted7":  {body: "doctor_mode: assisted\ndoctor_interval: 7d\n", wantDays: 7},
		"badmode":    {body: "doctor_mode: auto\n", wantErr: "doctor_mode"},
		"budget0":    {body: "doctor_budget: 0\n", wantErr: "doctor_budget"},
		"suffix":     {body: "doctor_interval: 7d\n", wantDays: 7},
		"bare":       {body: "doctor_interval: \"30\"\n", wantDays: 30},
		"disabled":   {body: "doctor_interval: \"0\"\n", wantDays: 0},
		"negative":   {body: "doctor_interval: -3d\n", wantErr: "doctor_interval"},
		"duration":   {body: "doctor_interval: 2w\n", wantErr: "doctor_interval"},
		"checks":     {body: "auto_repair: [nonstandard_field, duplicate_link]\n", wantDays: DefaultDoctorIntervalDays},
		"unknown":    {body: "auto_repair: [nonstandard_field, stale_open]\n", wantErr: `"stale_open"`},
		"no-all":     {body: "auto_repair: [all]\n", wantErr: `no "all"`},
		"judgement":  {body: "auto_repair: [map_misfit]\n", wantErr: `"map_misfit"`},
	} {
		eff, findings := resolveFile(t, tc.body)
		if tc.wantErr != "" {
			if len(findings) != 1 || !strings.Contains(findings[0], tc.wantErr) {
				t.Errorf("%s: findings = %v, want one containing %s", name, findings, tc.wantErr)
			}
			continue
		}
		if len(findings) != 0 {
			t.Errorf("%s: %v", name, findings)
			continue
		}
		if days, _ := eff.DoctorIntervalDays(); days != tc.wantDays {
			t.Errorf("%s: days = %d, want %d", name, days, tc.wantDays)
		}
	}
}

// An absent auto_repair means the default list, an explicit empty one means
// none, and a list means exactly that list (D323). The nil-versus-empty
// distinction is what the whole feature rests on, so it is pinned through the
// real settings.yaml parser rather than by building a KBSpec in Go.
func TestResolveAutoRepairDefaultVersusExplicit(t *testing.T) {
	for name, tc := range map[string]struct {
		body        string
		want        []string
		wantDefault bool
	}{
		"default_auto":   {body: "", want: DefaultAutoRepair, wantDefault: true},
		"explicit_empty": {body: "auto_repair: []\n", want: []string{}},
		"explicit_list":  {body: "auto_repair: [duplicate_link]\n", want: []string{"duplicate_link"}},
	} {
		eff, findings := resolveFile(t, tc.body)
		if len(findings) != 0 {
			t.Fatalf("%s: %v", name, findings)
		}
		got, isDefault := eff.AutoRepairChecks()
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

func TestResolveDoctorAutoInterval(t *testing.T) {
	for name, tc := range map[string]struct {
		body     string
		wantErr  string
		wantDays int
	}{
		"default":  {body: "", wantDays: DefaultDoctorAutoIntervalDays},
		"suffix":   {body: "doctor_auto_interval: 7d\n", wantDays: 7},
		"disabled": {body: "doctor_auto_interval: \"0\"\n", wantDays: 0},
		"bad":      {body: "doctor_auto_interval: 2w\n", wantErr: "doctor_auto_interval"},
	} {
		eff, findings := resolveFile(t, tc.body)
		if tc.wantErr != "" {
			if len(findings) != 1 || !strings.Contains(findings[0], tc.wantErr) {
				t.Errorf("%s: findings = %v, want one containing %s", name, findings, tc.wantErr)
			}
			continue
		}
		if days, _ := eff.DoctorAutoIntervalDays(); days != tc.wantDays || len(findings) != 0 {
			t.Errorf("%s: days = %d (findings %v), want %d", name, days, findings, tc.wantDays)
		}
	}
}

// repair_on_write absent follows auto_repair (D349); an explicit false is the
// opt-out. Pinned through the settings.yaml parser, as AutoRepairChecks is.
func TestResolveRepairOnWrite(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want bool
	}{
		"absent":          {"", true},
		"empty_auto":      {"auto_repair: []\n", false},
		"explicit_false":  {"repair_on_write: false\n", false},
		"true_empty_auto": {"auto_repair: []\nrepair_on_write: true\n", true},
		"explicit_list":   {"auto_repair: [duplicate_link]\n", true},
	} {
		eff, findings := resolveFile(t, tc.body)
		if len(findings) != 0 {
			t.Fatalf("%s: %v", name, findings)
		}
		if got := eff.RepairOnWriteEnabled(); got != tc.want {
			t.Errorf("%s: RepairOnWriteEnabled() = %v, want %v", name, got, tc.want)
		}
	}
}

func TestDoctorBudgetDefaults(t *testing.T) {
	five := 5
	if got := (KBSpec{}).DoctorBudgetItems(); got != DefaultDoctorBudget {
		t.Errorf("default budget = %d, want %d", got, DefaultDoctorBudget)
	}
	if got := (KBSpec{DoctorBudget: &five}).DoctorBudgetItems(); got != 5 {
		t.Errorf("explicit budget = %d, want 5", got)
	}
}
