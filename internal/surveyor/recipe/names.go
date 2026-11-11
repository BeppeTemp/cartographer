package recipe

import (
	"regexp"
	"strings"
)

// Names of recipes and connectors. TRAP (D1956): the unprefixed
// namespace is the operator's and core's. A name without "/" comes from the
// Surveyor config or is built in; a name with one is "<plugin>/<local>" and can
// only be built by plugin loading, never typed into recipes: or connectors: of
// the config (their validators refuse "/"). Without that rule a plugin could
// shadow an operator's recipe or a KB could be pointed at the wrong code.
// Qualified names are what a Source stores and what the registry and the
// connector set hold, whole, so every site that compares a Source's string
// works on them unchanged.
var (
	localRE = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	// Both parts start and end with [a-z0-9] (no "-" at an edge), so the alias
	// "<plugin>--<local>" has exactly one reading: "a-/b" and "a/-b" would both be
	// "a---b". The plugin part starts with a letter, as a plugin name does.
	qualifiedRE = regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]/[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
)

// MaxQualifiedLen caps a qualified name. The model is offered
// "<alias>__<tool>" under a provider's 64-character tool-name cap. The alias of
// a 31-character qualified name is 32 ("/" becomes "--"), which leaves 30
// characters for the tool; an operator-local connector name of 30 leaves 32.
// The budget is close to the local one, not equal. A plugin whose name leaves
// no room for its connectors' names is a plugin to rename.
const MaxQualifiedLen = 31

// Qualified joins a plugin name and a local name.
func Qualified(plugin, local string) string { return plugin + "/" + local }

// Split returns the plugin and the local name of a qualified name; ok is false
// for an unprefixed (operator-local or built-in) name or a malformed one.
func Split(name string) (plugin, local string, ok bool) {
	if !ValidQualified(name) {
		return "", "", false
	}
	plugin, local, _ = strings.Cut(name, "/")
	return plugin, local, true
}

// ValidLocal reports whether name is a valid unprefixed name (no "/").
func ValidLocal(name string) bool { return localRE.MatchString(name) }

// ValidQualified reports whether name is a valid "<plugin>/<local>" name.
func ValidQualified(name string) bool {
	return len(name) <= MaxQualifiedLen && qualifiedRE.MatchString(name)
}
