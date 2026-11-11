package core

import (
	"errors"
	"fmt"
	"sort"
)

// Pin is the configuration of a plugin in a headless component (the server or
// the Surveyor config): a digest the operator wrote, no install state. It is an
// Entry, so State stays the one rule that decides whether a plugin runs.
type Pin struct {
	// Enabled false skips the plugin; the zero value is therefore "off".
	Enabled bool
	// Digest is the tree digest the operator approved: 64 lowercase hex.
	Digest string
}

// PluginEnabled implements Entry.
func (p Pin) PluginEnabled() bool { return p.Enabled }

// PluginApproved implements Entry.
func (p Pin) PluginApproved() string { return p.Digest }

// PinProblem is a pin that cannot load: the caller refuses to start (fail closed).
type PinProblem struct {
	Name   string
	Reason string
}

func (p PinProblem) Error() string { return p.Name + ": " + p.Reason }

// LoadPinned opens every enabled pin of a component through a read-only store
// rooted at storeDir and returns the active plugins that have the component's
// side, in name order. Every enabled pin that does not load is a problem: its
// State when not active, or `no <side> side` for a plugin that never
// contributes to this component, or the side's unsupported reason. A malformed
// digest is a problem before any disk access. A disabled pin is skipped. An
// explicit pin is an assertion, so the caller must start with no problems.
func LoadPinned(storeDir string, pins map[string]Pin, side Side, opts Options) ([]*Plugin, []PinProblem) {
	names := make([]string, 0, len(pins))
	for n := range pins {
		names = append(names, n)
	}
	sort.Strings(names)
	store := Store{Root: storeDir, ReadOnly: true}
	var out []*Plugin
	var probs []PinProblem
	for _, n := range names {
		pin := pins[n]
		if !pin.Enabled {
			continue
		}
		if err := checkName(n); err != nil {
			probs = append(probs, PinProblem{n, "not a valid plugin name"})
			continue
		}
		if !digestRe.MatchString(pin.Digest) {
			probs = append(probs, PinProblem{n, "the digest is not 64 lowercase hex characters"})
			continue
		}
		st, p := State(n, pin, store, opts)
		switch {
		case st != StatusActive:
			probs = append(probs, PinProblem{n, string(st)})
		case !hasSide(p.Sides, side):
			probs = append(probs, PinProblem{n, fmt.Sprintf("no %s side", side)})
		case p.Unsupported[side] != "":
			probs = append(probs, PinProblem{n, fmt.Sprintf("the %s side cannot load here: %s", side, p.Unsupported[side])})
		default:
			out = append(out, p)
		}
	}
	return out, probs
}

// JoinPinProblems folds the problems into one error, nil for none.
func JoinPinProblems(probs []PinProblem) error {
	errs := make([]error, len(probs))
	for i, p := range probs {
		errs[i] = p
	}
	return errors.Join(errs...)
}

func hasSide(sides []Side, s Side) bool {
	for _, x := range sides {
		if x == s {
			return true
		}
	}
	return false
}
