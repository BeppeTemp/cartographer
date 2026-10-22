package skillbundle

// legacyNames maps the name a bundled skill had before D958 to the name it has
// now. It is the one place that knows the old names: skill_install, the
// legacy_skill_name lint check and the sync warning all read it. A name is
// never removed from it, because a KB copy made years ago still carries it.
var legacyNames = map[string]string{
	"kb-conflict-resolve": "cartographer-kb-conflict-resolve",
	"kb-create":           "cartographer-kb-create",
	"kb-doctor":           "cartographer-kb-doctor",
	"kb-import":           "cartographer-kb-import",
	"kb-ingest":           "cartographer-kb-ingest",
}

// Renamed returns the current name of a bundled skill that was known by name
// before D958.
func Renamed(name string) (current string, ok bool) {
	current, ok = legacyNames[name]
	return current, ok
}

// LegacyNames returns a copy of the old-name → current-name map.
func LegacyNames() map[string]string {
	out := make(map[string]string, len(legacyNames))
	for old, current := range legacyNames {
		out[old] = current
	}
	return out
}
