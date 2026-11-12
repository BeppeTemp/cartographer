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
	for _, internal := range []string{selApply, selStop, selOpenKB, selMenuNeedsUpdate, selMenuWillOpen, selMenuDidClose} {
		if seenSel[internal] {
			t.Errorf("internal selector %q collides with an action selector", internal)
		}
	}
	// The delegate methods and the internal selectors are all different too:
	// one method table registers them on the same class.
	all := map[string]bool{}
	for _, sel := range []string{selApply, selStop, selOpenKB, selMenuNeedsUpdate, selMenuWillOpen, selMenuDidClose} {
		if all[sel] {
			t.Errorf("selector %q is used twice on the target class", sel)
		}
		all[sel] = true
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
