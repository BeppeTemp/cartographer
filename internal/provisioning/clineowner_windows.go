//go:build windows

package provisioning

import "os"

// ownedByCurrentUser: a Windows temp dir is per user already, and the POSIX
// owner is not readable there.
func ownedByCurrentUser(os.FileInfo) bool { return true }
