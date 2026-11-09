package system

import (
	"os"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

// TestMain: see gitxtest.QuietGit (the store tests build a KB).
func TestMain(m *testing.M) {
	gitxtest.QuietGit()
	os.Exit(m.Run())
}
