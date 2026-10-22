package gitx

import (
	"os"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

// TestMain: see gitxtest.QuietGit.
func TestMain(m *testing.M) {
	gitxtest.QuietGit()
	os.Exit(m.Run())
}
