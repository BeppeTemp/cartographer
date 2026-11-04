//go:build !windows

package provisioning

import (
	"os"
	"syscall"
)

// ownedByCurrentUser reports whether fi is owned by the user running us.
func ownedByCurrentUser(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
