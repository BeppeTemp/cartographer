package plugin

import (
	"path"
	"strings"
)

// GlobMatch reports whether p, a path in the placeholder form
// ("{home}/notes/a.md"), matches glob, a manifest file glob. The dialect: the
// path is split on "/"; a segment of the glob is matched as path.Match does
// (* ? [..] within one segment, never across a "/"), and a segment that is
// exactly ** matches zero or more whole segments. Both are compared as
// written: the anchor is part of the first segment.
func GlobMatch(glob, p string) bool {
	return matchSegs(strings.Split(glob, "/"), strings.Split(p, "/"))
}

func matchSegs(g, p []string) bool {
	for len(g) > 0 {
		if g[0] == "**" {
			for len(g) > 1 && g[1] == "**" {
				g = g[1:]
			}
			if len(g) == 1 {
				return len(p) > 0
			}
			for i := 0; i <= len(p); i++ {
				if matchSegs(g[1:], p[i:]) {
					return true
				}
			}
			return false
		}
		if len(p) == 0 {
			return false
		}
		if ok, err := path.Match(g[0], p[0]); err != nil || !ok {
			return false
		}
		g, p = g[1:], p[1:]
	}
	return len(p) == 0
}
