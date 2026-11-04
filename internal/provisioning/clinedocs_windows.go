//go:build windows

package provisioning

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// clineDocumentsDir is the Documents folder Cline reads its global hooks from.
// Cline asks PowerShell for the MyDocuments known folder, so a OneDrive-
// redirected folder is honoured: for the real home directory the answer is the
// same known folder; for any other base dir (every test) it stays home-relative.
func clineDocumentsDir(baseDir string) string {
	if home, err := os.UserHomeDir(); err == nil && filepath.Clean(home) == filepath.Clean(baseDir) {
		if p, err := windows.KnownFolderPath(windows.FOLDERID_Documents, 0); err == nil && p != "" {
			return p
		}
	}
	return filepath.Join(baseDir, "Documents")
}
