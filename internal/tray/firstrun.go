package tray

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// FirstStartHere reports whether this is the first tray start from the
// executable exe under stateDir, and records it: the answer is true once per
// path, then false forever. Windows keeps "show the icon in the taskbar" per
// executable path, so an icon from a path it has not seen lands in the (^)
// overflow, and the tray says so once with a balloon (#1946). The marker is a
// file named after a hash of the path (case-folded: Windows paths are), so a
// binary moved to another directory announces itself again and an update in
// place does not. Any error answers false: a notification the user cannot
// silence must not repeat because the state directory is unwritable.
func FirstStartHere(stateDir, exe string) bool {
	if stateDir == "" || exe == "" {
		return false
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(exe))))
	dir := filepath.Join(stateDir, "tray-announced")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	f, err := os.OpenFile(filepath.Join(dir, hex.EncodeToString(sum[:8])), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	_, _ = f.WriteString(exe + "\n")
	return f.Close() == nil
}
