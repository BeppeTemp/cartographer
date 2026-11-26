package configurator

import (
	"path/filepath"
	"sort"
	"strings"
)

// rootDirs are the top-level (relative to the client base dir) directories every
// provider's artifacts live under: the boundaries the prune never removes on its
// own, even left completely empty (D63); the created-files ledger records them
// and every level below them the client makes (D2409 amended). They are
// provisioning's own "mount points" and, for .claude/.codex/.opencode, often hold
// user files (settings.json, config.toml, opencode.json) the prune has no business
// touching. The levels underneath them (".claude/skills/<name>", then
// ".claude/skills") go when empty only if the ledger lists them as created.
// TestEveryDestinationRootIsAPruneBoundary checks it against both destination
// matrices: a provider or a workspace cell added later fails there until its root
// is here. Writers outside the matrix (hook registrations, bootstrap) sit under
// these too. Keys are slash paths.
var rootDirs = map[string]bool{
	".claude":          true,
	".codex":           true,
	".kiro":            true,
	".opencode":        true,
	".config":          true,
	".config/opencode": true,
	".config/crush":    true,
	".gemini":          true,
	".copilot":         true,
	".pi":              true,
	// .cline also holds Cline's own data/ (settings, sessions): the boundary
	// keeps it, and only the levels created underneath are pruned.
	".cline": true,
	// Workspace scope only (projectDestinationMatrix).
	".agents": true,
	".crush":  true,
	// .github is the repository's own directory (workflows, templates): only
	// the levels Cartographer created under it are ever pruned.
	".github": true,
}

// IsRootDir reports whether rel (relative to a client base dir, slash or OS
// separator) is one of the provisioning root directories.
func IsRootDir(rel string) bool {
	return rootDirs[filepath.ToSlash(rel)]
}

// RootDirs lists the provisioning root directories as slash paths, sorted.
func RootDirs() []string {
	out := make([]string, 0, len(rootDirs))
	for r := range rootDirs {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// validLedgerDir reports whether a ledger entry may be acted on: a relative,
// local slash path (D2409 amended). The ledger is
// untrusted input, as the lockfile is: a path that climbs or is absolute is
// dropped, and an entry only lets a prune remove an empty directory that the climb
// from a managed file reaches.
func validLedgerDir(rel string) bool {
	if rel == "" || strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) || strings.Contains(rel, `\`) {
		return false
	}
	for _, el := range strings.Split(rel, "/") {
		if el == "" || el == "." || el == ".." {
			return false
		}
	}
	return true
}
