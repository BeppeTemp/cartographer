package main

import (
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
)

func TestNewKBSettingsFile_IsAlignedAtBirth(t *testing.T) {
	for _, branch := range []string{"", "team"} {
		data := newKBSettingsFile(branch)
		out, rep, err := config.RealignKBSettings(data)
		if err != nil {
			t.Fatal(err)
		}
		if !rep.Empty() {
			t.Errorf("branch %q: a new settings.yaml would be realigned on first serve: %v\n--- got\n%s\n--- want\n%s", branch, rep.Lines(), out, data)
		}
	}
}
