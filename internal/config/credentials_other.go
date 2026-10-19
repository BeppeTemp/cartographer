//go:build !unix

package config

import "os"

// ownedByProcess has no owner to compare off Unix; LoosePermissions does not
// get here on Windows, which has no such mode.
func ownedByProcess(os.FileInfo) bool { return true }
