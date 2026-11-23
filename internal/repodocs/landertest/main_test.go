package landertest

import (
	"os"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

// The tests build git repositories: no detached `git gc --auto` may write into one while
// t.TempDir removes it (internal/repodocs checks that every such package says so).
func TestMain(m *testing.M) {
	gitxtest.QuietGit()
	os.Exit(m.Run())
}
