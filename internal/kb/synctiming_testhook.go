package kb

import "time"

// SetSyncOutDebounceForTest overrides the package push-debounce window and
// returns a function that restores it. #903 WP3 turned the window into a
// constant backed by a package var (gitsync.go); a test in another package
// (mcpserver) that needs to keep the async worker from firing, or to make it
// fire quickly, sets it through here. Not for production use: the window is a
// constant.
func SetSyncOutDebounceForTest(d time.Duration) (restore func()) {
	old := syncOutDebounce
	syncOutDebounce = d
	return func() { syncOutDebounce = old }
}

// SetSyncInWindowForTest overrides the package SyncIn freshness window and
// returns a function that restores it. See SetSyncOutDebounceForTest.
func SetSyncInWindowForTest(d time.Duration) (restore func()) {
	old := syncInWindow
	syncInWindow = d
	return func() { syncInWindow = old }
}
