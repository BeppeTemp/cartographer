//go:build windows

package star

// openNoFollow is 0 on Windows: it has no such flag, and safewrite.CheckPath
// refuses a reparse point (a link or a junction) before the open.
const openNoFollow = 0
