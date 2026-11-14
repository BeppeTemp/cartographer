//go:build windows

package kb

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// readRetrySleep is the pause between read attempts; a seam so a test does not
// depend on the wall clock.
var readRetrySleep = time.Sleep

// readFileRetrying reads a file that a writer may be renaming over (#2077).
// Go opens with FILE_SHARE_READ|FILE_SHARE_WRITE and no FILE_SHARE_DELETE, so
// an open that meets the writer's rename fails with ERROR_SHARING_VIOLATION
// for microseconds. The read is retried up to 6 times with a doubling pause
// from 1 ms (about 63 ms in all) on that error only: ERROR_ACCESS_DENIED is
// also a genuine permission error and is not retried here.
func readFileRetrying(path string) ([]byte, error) {
	delay := time.Millisecond
	for attempt := 1; ; attempt++ {
		data, err := os.ReadFile(path)
		if err == nil || attempt == 6 || !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return data, err
		}
		readRetrySleep(delay)
		delay *= 2
	}
}
