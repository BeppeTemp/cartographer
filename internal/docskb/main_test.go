package docskb

import (
	"os"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

// TestMain: see gitxtest.QuietGit.
func TestMain(m *testing.M) {
	gitxtest.QuietGit()
	code := m.Run()
	closeSharedRunner()
	os.Exit(code)
}
