//go:build unix

package config

import (
	"os"
	"syscall"
)

// ownedByProcess says whether the server's user owns the file. Without an
// owner to read it counts as owned, so the group check stays on.
func ownedByProcess(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return !ok || int(st.Uid) == processUID()
}
