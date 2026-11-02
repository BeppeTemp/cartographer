package settingschema

import (
	"os"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

// TestMain: see gitxtest.QuietGit. The description list test loads the schemas of
// the KB package, so the rule for packages whose tests import it applies.
func TestMain(m *testing.M) {
	gitxtest.QuietGit()
	os.Exit(m.Run())
}
