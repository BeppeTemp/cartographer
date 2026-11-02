//go:build darwin

package tray

import "testing"

// Every menu action that can be chosen has exactly one target selector, and the
// selectors are distinct (a duplicate would silently route two rows to one).
func TestSelectorActionsCoverEveryAction(t *testing.T) {
	seenSel := map[string]bool{}
	seenAct := map[Action]bool{}
	for _, sa := range selectorActions {
		if seenSel[sa.sel] {
			t.Errorf("selector %q listed twice", sa.sel)
		}
		seenSel[sa.sel] = true
		seenAct[sa.action] = true
	}
	for _, a := range []Action{ActionOpenAtlas, ActionRestart, ActionOpenLog, ActionQuit} {
		if !seenAct[a] {
			t.Errorf("action %d has no selector", a)
		}
	}
	if seenSel[selApply] || seenSel[selStop] {
		t.Error("an internal selector collides with an action selector")
	}
}

func TestSelectorZeroForSeparator(t *testing.T) {
	if s0("") != 0 {
		t.Error("an empty selector name must map to the nil SEL")
	}
}

// The icon is rendered at 2x the point size the status item is given.
func TestIconPixelsAreDoublePoints(t *testing.T) {
	if iconPixels != 2*iconPoints {
		t.Errorf("iconPixels = %d, want %d", iconPixels, 2*iconPoints)
	}
}
