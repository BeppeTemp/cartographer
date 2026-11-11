package plugin

import (
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// coreFiles are the files WP2 moves to internal/plugin/core, the half of the
// plugin system the server and the Surveyor link. None of them may import
// internal/provisioning (its subpackage contract is fine): provisioning writes
// into other people's home directories. Temporary: deleted by the move.
var coreFiles = []string{
	"manifest.go", "version.go", "family.go", "state.go", "pinned.go", "store.go", "tree.go",
	"scripts.go", "provider_spec.go", "provider_resolve.go", "anchors.go", "points.go", "folder.go",
}

func TestCoreFilesDoNotImportProvisioning(t *testing.T) {
	for _, f := range coreFiles {
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, im := range af.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			if p == "github.com/BeppeTemp/cartographer/internal/provisioning" {
				t.Errorf("%s imports internal/provisioning", f)
			}
		}
	}
}
