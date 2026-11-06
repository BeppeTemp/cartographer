package configurator

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// The path rules of a plugin provider's MCP files. They live here, below
// provisioning, because the registry (buildPlugin) and the writers (applyGlob,
// removeStale) must be able to apply them without importing it, and provisioning
// reads the same lists for the cells (DeniedClientDir, ClientDir).

// deniedGlobalPrefixes: a global path starting with one of these segment lists
// is refused (home-relative, so .config/git is ~/.config/git). They name the
// places where a write is code execution or credentials theft the user would not
// think to look for in a client's own directory. Matching is segment-wise and
// case-insensitive on every OS, after anchor resolution.
var deniedGlobalPrefixes = [][]string{
	{".ssh"}, {".gnupg"}, {".aws"}, {".kube"}, {".docker"}, {"desktop"},
	{"documents", "powershell"}, {"documents", "windowspowershell"},
	{"appdata", "roaming", "microsoft"}, {"library", "launchagents"}, {"library", "launchdaemons"},
	{".config", "git"}, {".config", "autostart"}, {".config", "systemd"}, {".config", "environment.d"},
	{".config", "fish"}, {".config", "powershell"}, {".config", "nushell"},
	{".oh-my-zsh"}, {".bashrc.d"}, {".zshrc.d"}, {".profile.d"}, {".config.d"},
	{".cargo"}, {".npm-global"}, {".local", "bin"},
}

// foldCase lowercases a path off Linux, where the default file systems do not
// tell the spellings apart.
func foldCase(s string) string {
	if runtime.GOOS != "linux" {
		return strings.ToLower(s)
	}
	return s
}

func hasPrefixFold(segs, prefix []string) bool {
	if len(segs) < len(prefix) {
		return false
	}
	for i, p := range prefix {
		if !strings.EqualFold(segs[i], p) {
			return false
		}
	}
	return true
}

// ClientDir is the directory a global path belongs to a client: the first
// segment under the home, or the first two where one directory per client sits
// under a shared parent (.config, Documents), or the first three under the
// per-user data roots (.local/share, Library/Application Support, AppData/Local).
func ClientDir(segs []string) []string {
	n := 1
	if len(segs) > 0 {
		switch strings.ToLower(segs[0]) {
		case ".config", "documents":
			n = 2
		case ".local", "library", "appdata":
			n = 3
		}
	}
	return segs[:min(n, len(segs))]
}

// DeniedClientDir reports whether a global path is one no plugin provider may
// write under: the list above, git configuration (.git*), and Cartographer's own
// state and config (a client directory whose last segment starts with
// cartographer). dir is ClientDir(segs).
func DeniedClientDir(segs, dir []string) bool {
	if len(dir) == 0 {
		return true
	}
	for _, p := range deniedGlobalPrefixes {
		if hasPrefixFold(segs, p) {
			return true
		}
	}
	first, last := strings.ToLower(dir[0]), strings.ToLower(dir[len(dir)-1])
	return strings.HasPrefix(first, ".git") || strings.HasPrefix(last, ".cartographer") || strings.HasPrefix(last, "cartographer")
}

// builtinClientDirs are the client directories of the built-in providers, from
// their MCP file and config directories, keyed by folded slash path.
func builtinClientDirs() map[string]string {
	out := map[string]string{}
	add := func(p Provider, segs []string) {
		if len(segs) > 0 {
			out[foldCase(strings.Join(ClientDir(segs), "/"))] = string(p)
		}
	}
	for _, d := range Providers() {
		if d.MCPConfigPath != "" {
			add(d.Provider, strings.Split(d.MCPConfigPath, "/"))
		}
		for _, segs := range d.ConfigDirs {
			add(d.Provider, segs)
		}
		// ConfigDirGlobs are detection evidence in a shared editor folder
		// (.vscode/extensions), not a client directory of their own.
	}
	return out
}

// globSegment returns the index of the segment holding a `*`, -1 for none.
func globSegment(segs []string) int {
	for i, s := range segs {
		if strings.Contains(s, "*") {
			return i
		}
	}
	return -1
}

// segOverlap reports whether two path segments can name the same entry: equal
// (case-folded) or one a pattern matching the other. Two patterns overlap when
// the text before the first `*` of one starts the other's (conservative).
func segOverlap(a, b string) bool {
	if strings.EqualFold(a, b) {
		return true
	}
	pa, pb := strings.Contains(a, "*"), strings.Contains(b, "*")
	switch {
	case pa && pb:
		ha, hb := strings.ToLower(a[:strings.Index(a, "*")]), strings.ToLower(b[:strings.Index(b, "*")])
		return strings.HasPrefix(ha, hb) || strings.HasPrefix(hb, ha)
	case pa:
		ok, _ := filepath.Match(strings.ToLower(a), strings.ToLower(b))
		return ok
	case pb:
		ok, _ := filepath.Match(strings.ToLower(b), strings.ToLower(a))
		return ok
	}
	return false
}

// pathsOverlap reports whether two segment lists can name the same file.
func pathsOverlap(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !segOverlap(a[i], b[i]) {
			return false
		}
	}
	return true
}

// checkPluginGlobalPath applies the global-cell checks to one MCP path of a
// plugin provider: at most one `*`, in one segment, never inside the client
// directory; below a client directory of its own (not the home, not a shared
// parent), not denied, not a built-in client's directory, no .git.
func checkPluginGlobalPath(label string, segs []string, builtin map[string]string) error {
	p := strings.Join(segs, "/")
	if strings.Count(p, "*") > 1 {
		return fmt.Errorf("%s %q holds more than one `*`: a pattern has one, in one segment", label, p)
	}
	cd := ClientDir(segs)
	fixed, star := segs, globSegment(segs)
	if star >= 0 {
		if star < len(cd) {
			return fmt.Errorf("%s %q: the `*` replaces part of the client directory %q, so it would span other applications' files", label, p, strings.Join(cd, "/"))
		}
		fixed = segs[:star]
	} else if len(segs) <= len(cd) {
		return fmt.Errorf("%s %q names no path below its client directory (a file directly in the home or in a shared parent is never a provider's)", label, p)
	}
	if DeniedClientDir(fixed, cd) {
		return fmt.Errorf("%s %q is under %q, a directory no plugin provider may write into", label, p, strings.Join(cd, "/"))
	}
	if owner, ok := builtin[foldCase(strings.Join(cd, "/"))]; ok {
		return fmt.Errorf("%s %q is under %q, the client directory of %s", label, p, strings.Join(cd, "/"), owner)
	}
	return nil
}

// configFixedDir is the directory part of an MCP file before any `*`: the
// directory a stale file must lie in.
func configFixedDir(config []string) []string {
	if i := globSegment(config); i >= 0 {
		return config[:i]
	}
	return config[:max(len(config)-1, 0)]
}

// CheckPluginMCPPaths validates the MCP paths of a plugin provider (home-relative
// segments): config is its own MCP file or pattern (nil keeps the base's), stale
// the files it asks to have removed after a write. Each must pass the global-cell
// checks; neither may equal or match a built-in provider's MCP file; and a stale
// file must lie below the fixed directory of config (the part before any `*`),
// never be config itself, and exists only with a config of its own. It is applied
// where the descriptor is resolved and where it is registered, and again before
// anything is removed.
func CheckPluginMCPPaths(name string, config []string, stale [][]string) error {
	if len(config) == 0 && len(stale) == 0 {
		return nil
	}
	builtin := builtinClientDirs()
	var files [][]string
	for _, d := range Providers() {
		if d.MCPConfigPath != "" {
			files = append(files, strings.Split(d.MCPConfigPath, "/"))
		}
	}
	notBuiltin := func(label string, segs []string) error {
		for _, f := range files {
			if pathsOverlap(segs, f) {
				return fmt.Errorf("plugin provider %q: %s %q is the MCP file of a built-in provider", name, label, strings.Join(segs, "/"))
			}
		}
		return nil
	}
	if len(config) > 0 {
		if err := checkPluginGlobalPath("mcp.config", config, builtin); err != nil {
			return fmt.Errorf("plugin provider %q: %w", name, err)
		}
		if err := notBuiltin("mcp.config", config); err != nil {
			return err
		}
	}
	if len(stale) > 0 && len(config) == 0 {
		return fmt.Errorf("plugin provider %q: mcp.stale needs an mcp.config of its own: a stale file is one the client derives from it", name)
	}
	dir := configFixedDir(config)
	for _, s := range stale {
		if err := checkPluginGlobalPath("mcp.stale", s, builtin); err != nil {
			return fmt.Errorf("plugin provider %q: %w", name, err)
		}
		if err := notBuiltin("mcp.stale", s); err != nil {
			return err
		}
		if len(s) <= len(dir) || !hasPrefixFold(s, dir) {
			return fmt.Errorf("plugin provider %q: mcp.stale %q does not lie below %q, the directory of mcp.config", name, strings.Join(s, "/"), strings.Join(dir, "/"))
		}
		if pathsOverlap(s, config) {
			return fmt.Errorf("plugin provider %q: mcp.stale %q is mcp.config itself", name, strings.Join(s, "/"))
		}
	}
	return nil
}

// IsBuiltinMCPFile reports whether the home-relative path rel (OS separators) is
// the MCP file of a built-in provider.
func IsBuiltinMCPFile(rel string) bool {
	segs := strings.Split(filepath.ToSlash(rel), "/")
	for _, d := range Providers() {
		if d.MCPConfigPath != "" && pathsOverlap(segs, strings.Split(d.MCPConfigPath, "/")) {
			return true
		}
	}
	return false
}
