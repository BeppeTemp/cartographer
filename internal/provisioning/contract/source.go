package contract

import (
	"strings"
)

// PluginSourcePrefix opens the Source of an artifact a plugin contributes
// ("plugin:<name>"). Such an artifact is built on the client from the approved
// tree of an installed plugin (never served, never signed): it is authorized
// only while its plugin is in ApplyOptions.ActivePlugins, and nothing else
// about it is trusted. No site that tests a KB or bundle source treats it as
// either (source_audit_test.go classifies every one of them).
const PluginSourcePrefix = "plugin:"

// IsPluginSource reports whether s is "plugin:<name>" with a valid name.
func IsPluginSource(s string) bool {
	name, ok := strings.CutPrefix(s, PluginSourcePrefix)
	return ok && ValidArtifactName(name)
}
