package kb

import (
	"os"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

// TestMain: see gitxtest.QuietGit. The git deadlines are scaled for the tests that
// are not about them (gitxtest.Deadline, #1048): on the loaded Windows runner a local
// file:// fetch outlived the bare 15 s FetchTimeout (#2075, the same as #2035 in
// internal/gitx). A test that is about a deadline sets its own value and restores
// this one (pushtimeout_test.go).
func TestMain(m *testing.M) {
	gitxtest.QuietGit()
	gitx.FetchTimeout = gitxtest.Deadline(gitx.FetchTimeout)
	gitx.PushTimeout = gitxtest.Deadline(gitx.PushTimeout)
	gitx.CommitTimeout = gitxtest.Deadline(gitx.CommitTimeout)
	os.Exit(m.Run())
}
