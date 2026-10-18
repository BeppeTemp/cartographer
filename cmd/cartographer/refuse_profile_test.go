package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// TestRefuseServerProfile checks that a KB whose settings.yaml still names the
// removed server git profile cannot be mounted, with a message that names the
// way out, and that a KB without it mounts (#903 WP2).
func TestRefuseServerProfile(t *testing.T) {
	write := func(t *testing.T, body string) string {
		t.Helper()
		root := t.TempDir()
		if body != "" {
			if err := os.WriteFile(filepath.Join(root, kb.SettingsFile), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}

	// A KB still on the profile is refused, and the message guides the user.
	err := refuseServerProfile(write(t, "git_profile: server\n"))
	if err == nil {
		t.Fatal("a KB with git_profile: server must be refused")
	}
	for _, want := range []string{"git_profile", "#903", "branch"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message should mention %q: %v", want, err)
		}
	}

	// A KB without the key, with another key, or with no settings file mounts.
	for _, body := range []string{"", "usage_stale_days: 30\n", "git_profile: \"\"\n"} {
		if err := refuseServerProfile(write(t, body)); err != nil {
			t.Errorf("a KB without the profile must mount, body %q: %v", body, err)
		}
	}
}
