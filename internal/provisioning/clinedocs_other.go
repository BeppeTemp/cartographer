//go:build !windows

package provisioning

import "path/filepath"

// clineDocumentsDir is the Documents folder Cline reads its global hooks from
// (Documents/Cline/Hooks). Home-relative here: Cline asks `xdg-user-dir` on
// Linux and uses $HOME/Documents on macOS, and a relocated Linux folder is a
// documented limitation (docs/harnesses.md). A file pair, not a runtime.GOOS
// branch (docs/conventions.md §Dependencies).
func clineDocumentsDir(baseDir string) string {
	return filepath.Join(baseDir, "Documents")
}
