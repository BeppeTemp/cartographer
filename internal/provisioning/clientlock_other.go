//go:build !unix

package provisioning

import "os"

// The supported platforms are darwin and linux (see internal/service's goos
// switch). Elsewhere the lock degrades to a no-op rather than failing every
// sync: an unsupported platform gets today's behaviour, not a broken client.
func tryLockFile(*os.File) error { return nil }

func unlockFile(*os.File) error { return nil }

func isLockBusy(error) bool { return false }
