//go:build !windows

package kb

// restoreMayHeal is false off Windows: a file is never held open against git
// there, so a refusal without a lock does not clear by itself (#2254).
func restoreMayHeal(error) bool { return false }
