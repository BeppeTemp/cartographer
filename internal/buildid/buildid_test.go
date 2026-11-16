package buildid

import (
	"runtime/debug"
	"strings"
	"testing"
)

func setting(k, v string) debug.BuildSetting { return debug.BuildSetting{Key: k, Value: v} }

func TestFromInfo(t *testing.T) {
	for name, tc := range map[string]struct {
		settings []debug.BuildSetting
		want     string
	}{
		"clean revision":       {[]debug.BuildSetting{setting("vcs.revision", "abc123"), setting("vcs.modified", "false")}, "vcs:abc123"},
		"modified tree":        {[]debug.BuildSetting{setting("vcs.revision", "abc123"), setting("vcs.modified", "true")}, ""},
		"no vcs stamping":      {nil, ""},
		"modified flag absent": {[]debug.BuildSetting{setting("vcs.revision", "abc123")}, "vcs:abc123"},
	} {
		if got := fromInfo(&debug.BuildInfo{Settings: tc.settings}); got != tc.want {
			t.Errorf("%s: fromInfo = %q, want %q", name, got, tc.want)
		}
	}
}

func TestSelfIsStableAndNamesTheTestBinary(t *testing.T) {
	// A test binary carries no VCS stamp: the file hash is the identity.
	got := Self()
	if !strings.HasPrefix(got, hashPrefix) || got != Self() {
		t.Errorf("Self() = %q, want a stable %s<hex>", got, hashPrefix)
	}
}

func TestSame(t *testing.T) {
	for name, tc := range map[string]struct {
		a, b string
		want bool
	}{
		"equal":                 {"vcs:a", "vcs:a", true},
		"different revisions":   {"vcs:a", "vcs:b", false},
		"revision against hash": {"vcs:a", "sha256:a", false},
		"server without field":  {"", "vcs:a", true},
		"binary without name":   {"vcs:a", "", true},
	} {
		if got := Same(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: Same(%q, %q) = %v, want %v", name, tc.a, tc.b, got, tc.want)
		}
	}
}
