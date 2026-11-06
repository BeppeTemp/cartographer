package plugin

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// ScriptEntry is the plugin's script at the root: the one file that says what
	// the plugin registers.
	ScriptEntry = "plugin.star"
	// LibDir holds the flat *.star files plugin.star may load.
	LibDir = "lib"
	// MaxScriptBytes caps one script file; MaxLibFiles the files of lib/.
	MaxScriptBytes = 256 << 10
	MaxLibFiles    = 32
)

var libFileRe = regexp.MustCompile(`^lib/[A-Za-z0-9_-]+\.star$`)

// IsLibPath reports a path that names a loadable lib file.
func IsLibPath(path string) bool { return libFileRe.MatchString(path) }

// IsScriptPath reports a path the tree reader treats as script territory:
// plugin.star itself or anything under lib/ (or a path that starts like them).
func IsScriptPath(path string) bool {
	if path == ScriptEntry {
		return true
	}
	first, _, nested := strings.Cut(path, "/")
	return first == ScriptEntry || (first == LibDir && (nested || path == LibDir))
}

// scriptProblem is why a script-territory file is refused, "" when it is fine.
func scriptProblem(path string, size int) string {
	switch {
	case path != ScriptEntry && !IsLibPath(path):
		return "a plugin holds plugin.star at its root and lib/<name>.star files directly in lib/"
	case size > MaxScriptBytes:
		return fmt.Sprintf("%d bytes, over the %d cap of a script", size, MaxScriptBytes)
	}
	return ""
}

// scriptTreeProblems checks the script files of a tree as a set; ReadTree runs
// opts.CheckScripts on them only once the rest of the tree is valid.
func scriptTreeProblems(scripts []TreeFile) []string {
	var problems []string
	entry, libs := false, 0
	for _, f := range scripts {
		if f.Path == ScriptEntry {
			entry = true
		} else {
			libs++
		}
	}
	if libs > 0 && !entry {
		problems = append(problems, "lib/: a plugin with lib files needs a plugin.star at its root")
	}
	if libs > MaxLibFiles {
		problems = append(problems, fmt.Sprintf("lib/: %d files, over the %d cap", libs, MaxLibFiles))
	}
	return problems
}
