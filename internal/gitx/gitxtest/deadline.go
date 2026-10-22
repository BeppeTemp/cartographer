package gitxtest

import (
	"runtime"
	"time"
)

// Deadline is the wall-clock budget a test gives to something that waits on
// git or on the server (a commit, a push, a debounced repair run, one MCP
// response): d on a normal machine, four times d on Windows. The
// test-windows runners take 1-2 s for a single commit and run the shards
// under load, so a bare 5 s or 20 s that never fails on Linux fails there
// without any bug (#1048). Wrap every deadline of that kind in it, in
// `time.After(gitxtest.Deadline(...))` or `time.Now().Add(...)`, instead of
// raising one number after each red run. A wait that fires the instant the
// event happens (a channel, a condition polled until it holds) costs nothing
// when the scale is generous; a deadline that is itself under test (a
// timeout the code must hit) is not scaled.
func Deadline(d time.Duration) time.Duration {
	if runtime.GOOS == "windows" {
		return 4 * d
	}
	return d
}
