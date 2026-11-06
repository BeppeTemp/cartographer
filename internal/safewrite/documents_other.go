//go:build !windows

package safewrite

import "path/filepath"

// DocumentsDir is the user's Documents folder, the one Cline reads its global
// hooks and rules from and the {documents} anchor of a plugin provider. It is
// home-relative here: Cline asks `xdg-user-dir` on Linux and uses $HOME/Documents
// on macOS, and a relocated Linux folder is a documented limitation
// (docs/harnesses.md). A file pair, not a runtime.GOOS branch
// (docs/conventions.md §Dependencies).
func DocumentsDir(home string) string {
	return filepath.Join(home, "Documents")
}
