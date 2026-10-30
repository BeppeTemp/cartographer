package recipe

import (
	"path"
	"strings"
)

// MatchGlob reports whether name (slash-separated) matches pattern: a "**"
// segment matches any number of segments, including none; every other segment
// is path.Match. The standard library has no such matcher.
func MatchGlob(pattern, name string) bool {
	return matchSegs(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegs(p, n []string) bool {
	for len(p) > 0 {
		if p[0] == "**" {
			for len(p) > 0 && p[0] == "**" {
				p = p[1:]
			}
			if len(p) == 0 {
				return true
			}
			for i := 0; i <= len(n); i++ {
				if matchSegs(p, n[i:]) {
					return true
				}
			}
			return false
		}
		if len(n) == 0 {
			return false
		}
		if ok, err := path.Match(p[0], n[0]); err != nil || !ok {
			return false
		}
		p, n = p[1:], n[1:]
	}
	return len(n) == 0
}

func matchAny(globs []string, name string) bool {
	for _, g := range globs {
		if MatchGlob(g, name) {
			return true
		}
	}
	return false
}
