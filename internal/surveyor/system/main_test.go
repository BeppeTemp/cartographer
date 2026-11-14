package system

import (
	"os"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

// TestMain: see gitxtest.QuietGit (the store tests build a KB). The git deadlines are
// scaled (gitxtest.Deadline, #1048) so that on a starved Windows runner the server's own
// 60 s commit bound does not become the next flake (#2065, as #2035 in internal/gitx).
func TestMain(m *testing.M) {
	gitxtest.QuietGit()
	gitx.FetchTimeout = gitxtest.Deadline(gitx.FetchTimeout)
	gitx.PushTimeout = gitxtest.Deadline(gitx.PushTimeout)
	gitx.CommitTimeout = gitxtest.Deadline(gitx.CommitTimeout)
	os.Exit(m.Run())
}
