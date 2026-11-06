//go:build !windows

package star

import "syscall"

// openNoFollow makes open(2) refuse a final symlink; the components above are
// checked by safewrite before the open.
const openNoFollow = syscall.O_NOFOLLOW
