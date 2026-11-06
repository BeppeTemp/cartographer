//go:build windows

package safewrite

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// DocumentsDir is the user's Documents folder, the one Cline reads its global
// hooks and rules from and the {documents} anchor of a plugin provider. Cline
// asks PowerShell for the MyDocuments known folder, so a OneDrive-redirected
// folder is honoured: for the real home directory the answer is the same known
// folder; for any other home (every test) it stays home-relative.
func DocumentsDir(home string) string {
	if real, err := os.UserHomeDir(); err == nil && filepath.Clean(real) == filepath.Clean(home) {
		if p, err := windows.KnownFolderPath(windows.FOLDERID_Documents, 0); err == nil && p != "" {
			return p
		}
	}
	return filepath.Join(home, "Documents")
}
