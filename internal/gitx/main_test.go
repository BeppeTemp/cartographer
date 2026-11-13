package gitx

import (
	"os"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

// TestMain: see gitxtest.QuietGit. The package's git deadlines are scaled for the
// tests that are not about them (gitxtest.Deadline, #1048): on the loaded Windows
// runner a local file:// fetch outlived the bare 15 s FetchTimeout (#2035). A test
// that is about a deadline sets its own value and restores this one.
func TestMain(m *testing.M) {
	gitxtest.QuietGit()
	FetchTimeout = gitxtest.Deadline(FetchTimeout)
	PushTimeout = gitxtest.Deadline(PushTimeout)
	CommitTimeout = gitxtest.Deadline(CommitTimeout)
	MoveHistoryTimeout = gitxtest.Deadline(MoveHistoryTimeout)
	os.Exit(m.Run())
}
