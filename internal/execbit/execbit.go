// Package execbit reports whether the host filesystem carries the POSIX
// execute bit, and is the one place that answers that question.
//
// Cartographer transports an Executable flag on every artifact file and KB
// asset: a skill's helper script, a hook's entry point, an asset such as
// check.sh are useless without it. On unix the flag is the file's 0o111 bits.
// NTFS has no such permission — Go's os.Chmod only toggles the read-only
// attribute there, and os.Stat reports 0o666 for every regular file — so on
// Windows the flag cannot round-trip through the filesystem. It still travels
// through the protocol and is still stored in the KB; what changes is that a
// file read back from a Windows disk never reports it, and a mode difference
// there is not drift (D215).
package execbit

import "io/fs"

// IsExecutable reports whether mode carries the execute bit, always false where
// the filesystem cannot represent one.
func IsExecutable(mode fs.FileMode) bool {
	return Supported && mode&0o111 != 0
}
