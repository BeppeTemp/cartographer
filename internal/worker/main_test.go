package worker

import (
	"os"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

// TestMain: see gitxtest.QuietGit (allow_test.go builds a KB).
func TestMain(m *testing.M) {
	gitxtest.QuietGit()
	os.Exit(m.Run())
}
