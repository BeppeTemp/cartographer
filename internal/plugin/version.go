package plugin

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/updatecheck"
)

// maxRangeTerms bounds the comparators of one range; a real range has one or two.
const maxRangeTerms = 8

var (
	// versionRe is X.Y.Z, three non-negative integers without leading zeros.
	versionRe = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)
	termRe    = regexp.MustCompile(`^(>=|<=|>|<|=)?(\d.*)$`)
	// coreRe takes the X.Y.Z core of a running version: a pre-release or a
	// `git describe` build of X.Y.Z counts as X.Y.Z.
	coreRe = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)`)
)

type term struct {
	op  string // ">=", ">", "<=", "<", "="
	ver string // X.Y.Z
}

// parseRange reads space-separated comparators, all of which must hold. A bare
// X.Y.Z means "=".
func parseRange(rng string) ([]term, error) {
	fields := strings.Fields(rng)
	if len(fields) == 0 {
		return nil, fmt.Errorf("the range is empty")
	}
	if len(fields) > maxRangeTerms {
		return nil, fmt.Errorf("the range has %d comparators, at most %d", len(fields), maxRangeTerms)
	}
	out := make([]term, 0, len(fields))
	for _, f := range fields {
		m := termRe.FindStringSubmatch(f)
		if m == nil || !versionRe.MatchString(m[2]) {
			return nil, fmt.Errorf("%q is not a comparator: use >=, >, <=, < or = followed by X.Y.Z", f)
		}
		op := m[1]
		if op == "" {
			op = "="
		}
		out = append(out, term{op: op, ver: m[2]})
	}
	return out, nil
}

// ValidRange reports why rng is not a version range, or nil.
func ValidRange(rng string) error {
	_, err := parseRange(rng)
	return err
}

// RangeMet reports whether the running Cartographer version satisfies rng.
// A running version that does not parse (a "dev" build) satisfies every range,
// and a pre-release or `git describe` build compares as its X.Y.Z core. A
// malformed range is an error.
func RangeMet(rng, running string) (bool, error) {
	terms, err := parseRange(rng)
	if err != nil {
		return false, err
	}
	m := coreRe.FindString(strings.TrimSpace(running))
	if m == "" {
		return true, nil
	}
	core := strings.TrimPrefix(m, "v")
	for _, t := range terms {
		c, ok := updatecheck.Cmp(core, t.ver)
		if !ok {
			return true, nil
		}
		var holds bool
		switch t.op {
		case ">=":
			holds = c >= 0
		case ">":
			holds = c > 0
		case "<=":
			holds = c <= 0
		case "<":
			holds = c < 0
		default:
			holds = c == 0
		}
		if !holds {
			return false, nil
		}
	}
	return true, nil
}
