//go:build windows

package execbit

// Supported is false: NTFS has no execute permission, so the bit can neither be
// set nor read back here.
const Supported = false
