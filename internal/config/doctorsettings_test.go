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

// The write gate, the safe repairs and the doctor's cadence are fixed (D907):
// a KB file that still carries one of their keys has it left out with a finding
// that says what replaced it, and it changes nothing.
func TestResolveRemovedDoctorKeysAreLeftOut(t *testing.T) {
	for _, body := range []string{
		"write_gate: error\n",
		"auto_repair: [duplicate_link]\n",
		"auto_repair: []\n",
		"repair_on_write: false\n",
		"doctor_auto_interval: 0\n",
		"doctor_interval: 7d\n",
		"doctor_mode: assisted\n",
		"doctor_budget: 4\n",
	} {
		key := strings.SplitN(body, ":", 2)[0]
		eff, findings := resolveFile(t, body)
		if len(findings) != 1 || !strings.Contains(findings[0], key) || !strings.Contains(findings[0], "D907") {
			t.Errorf("%s: findings = %v, want one naming the key and D907", key, findings)
		}
		if eff.UsageStale() != DefaultUsageStaleDays {
			t.Errorf("%s: the policy changed", key)
		}
	}
}
