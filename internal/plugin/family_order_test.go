package plugin

import "testing"

// Register (points.go init) asks FamilyOf, so the family table must be filled
// before any init runs: it is a package-level var built by expression, which Go
// initialises ahead of every init, whatever the file-name order.
func TestFamiliesAreFilledBeforeAnyInit(t *testing.T) {
	for _, dir := range []string{"skills", "agents", "hooks", "providers", LibDir, ScriptEntry, "presets", "roles"} {
		if _, ok := FamilyOf(dir); !ok {
			t.Errorf("no family owns %q", dir)
		}
	}
	for _, p := range RegisteredPoints() {
		if p.Family == "" {
			t.Errorf("point %s has no family", p.Dir)
		}
	}
}
