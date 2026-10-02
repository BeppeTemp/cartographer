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
		"checks":    {body: "kbs:\n  - path: /tmp/kb\n    doctor_auto_repair: [nonstandard_field, duplicate_link]\n", wantDays: DefaultDoctorIntervalDays},
		"unknown":   {body: "kbs:\n  - path: /tmp/kb\n    doctor_auto_repair: [nonstandard_field, stale_open]\n", wantErr: `"stale_open"`},
		"no-all":    {body: "kbs:\n  - path: /tmp/kb\n    doctor_auto_repair: [all]\n", wantErr: `no "all"`},
		"judgement": {body: "kbs:\n  - path: /tmp/kb\n    doctor_auto_repair: [map_misfit]\n", wantErr: `"map_misfit"`},
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
