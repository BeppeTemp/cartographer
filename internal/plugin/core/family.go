package core

import (
	"fmt"
	"maps"
	"slices"
	"sort"
)

// Family is a group of extension points a plugin side declares an sdk level
// for. A schema 2 manifest requires a family by naming it under
// components.<side>.sdk; the tree folders and files it owns are Dirs and Files.
type Family struct {
	Side  Side
	Name  string
	Level int // the highest level this build implements
	Dirs  []string
	Files []string
}

// families is built by expression, not by an init func: Go initialises
// package-level vars before any init, so points.go's init (which Register
// checks against FamilyOf) never runs before the table is filled, whatever the
// file-name order.
var families = builtinFamilies()

// RegisterFamily adds a family. It is init-time: a duplicate name on a side, an
// unknown side, a bad level or a top-level name already claimed panics, so two
// sides can never both own a file.
func RegisterFamily(f Family) { families = addFamily(families, f) }

func addFamily(list []Family, f Family) []Family {
	if !slices.Contains(Sides(), f.Side) || f.Name == "" || f.Level < 1 || len(f.Dirs)+len(f.Files) == 0 {
		panic(fmt.Sprintf("plugin: invalid family %q", f.Name))
	}
	for _, o := range list {
		if o.Side == f.Side && o.Name == f.Name {
			panic(fmt.Sprintf("plugin: family %s/%s registered twice", f.Side, f.Name))
		}
		for _, n := range append(slices.Clone(f.Dirs), f.Files...) {
			if slices.Contains(o.Dirs, n) || slices.Contains(o.Files, n) {
				panic(fmt.Sprintf("plugin: %q is claimed by two families", n))
			}
		}
	}
	return append(list, f)
}

// Families returns the registered families sorted by side, then name.
func Families() []Family {
	out := slices.Clone(families)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Side != out[j].Side {
			return slices.Index(Sides(), out[i].Side) < slices.Index(Sides(), out[j].Side)
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// FamilyOf returns the family that owns a top-level folder or file name.
func FamilyOf(top string) (Family, bool) {
	for _, f := range families {
		if slices.Contains(f.Dirs, top) || slices.Contains(f.Files, top) {
			return f, true
		}
	}
	return Family{}, false
}

// Supported reports why this build cannot give a side the sdk level of a
// family, or "" when it can.
func Supported(side Side, family string, level int) string {
	for _, f := range families {
		if f.Side == side && f.Name == family {
			if level > f.Level {
				return fmt.Sprintf("needs sdk %s %d (this build has %d)", family, level, f.Level)
			}
			return ""
		}
	}
	return "unknown family " + family
}

// unsupportedReason is the first reason (by family name) a side's declared sdk
// is out of reach of this build, or "".
func unsupportedReason(side Side, c Component) string {
	for _, fam := range slices.Sorted(maps.Keys(c.SDK)) {
		if why := Supported(side, fam, c.SDK[fam]); why != "" {
			return why
		}
	}
	return ""
}

func builtinFamilies() []Family {
	var l []Family
	l = addFamily(l, Family{Side: SideClient, Name: "artifacts", Level: 1, Dirs: []string{"skills", "agents", "hooks"}})
	l = addFamily(l, Family{Side: SideClient, Name: "providers", Level: 1, Dirs: []string{"providers"}})
	l = addFamily(l, Family{Side: SideClient, Name: "scripts", Level: 1, Dirs: []string{LibDir}, Files: []string{ScriptEntry}})
	l = addFamily(l, Family{Side: SideServer, Name: "presets", Level: 1, Dirs: []string{"presets"}})
	l = addFamily(l, Family{Side: SideServer, Name: "roles", Level: 1, Dirs: []string{"roles"}})
	l = addFamily(l, Family{Side: SideSurveyor, Name: "recipes", Level: 1, Dirs: []string{"recipes"}})
	l = addFamily(l, Family{Side: SideAtlas, Name: "views", Level: 1, Dirs: []string{"views"}})
	l = addFamily(l, Family{Side: SideAtlas, Name: "apps", Level: 1, Dirs: []string{"apps"}})
	l = addFamily(l, Family{Side: SideAtlas, Name: "branding", Level: 1, Dirs: []string{"branding"}})
	return l
}
