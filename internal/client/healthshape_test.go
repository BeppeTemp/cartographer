package client

import "testing"

// Any program can answer {"status":"ok"}: the version is what makes a /health
// body a Cartographer server's (#1998).
func TestIsServerHealth(t *testing.T) {
	for _, tc := range []struct {
		name            string
		status, version string
		want            bool
	}{
		{"ours", "ok", "1.2.3", true},
		{"a dev build", "ok", "dev", true},
		{"ok and nothing else", "ok", "", false},
		{"a version and another status", "degraded", "1.2.3", false},
		{"empty", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsServerHealth(tc.status, tc.version); got != tc.want {
				t.Errorf("IsServerHealth(%q, %q) = %v, want %v", tc.status, tc.version, got, tc.want)
			}
			if got := (&Health{Status: tc.status, Version: tc.version}).IsServer(); got != tc.want {
				t.Errorf("Health.IsServer = %v, want %v", got, tc.want)
			}
		})
	}
	var none *Health
	if none.IsServer() {
		t.Error("a nil Health is not a server")
	}
}
