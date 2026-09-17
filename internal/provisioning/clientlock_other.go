//go:build !unix && !windows

package provisioning

import "os"

// darwin, linux and windows each have a real implementation beside this file.
// Anywhere else the lock degrades to a no-op rather than failing every sync: an
// unsupported platform gets today's behaviour, not a broken client.
func tryLockFile(*os.File) error { return nil }

func unlockFile(*os.File) error { return nil }

func isLockBusy(error) bool { return false }
