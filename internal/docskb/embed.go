package docskb

import (
	"errors"
	"fmt"
	"os"

	cartographer "github.com/BeppeTemp/cartographer"
)

// Embedded generates the KB from the documentation embedded in this binary
// (the module-root DocsFS), pinned to the ref of version (Ref).
func Embedded(version string) (map[string][]byte, error) {
	return Generate(cartographer.DocsFS, Ref(version))
}

// Export writes the embedded KB tree into dir. It refuses an existing,
// non-empty directory so a stale tree is never mixed with a new one; a missing
// dir is created (0755). It makes no git repository: whoever serves the tree
// makes its own commit.
func Export(dir, version string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case err == nil && len(entries) > 0:
		return fmt.Errorf("export: %s exists and is not empty", dir)
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("export: %w", err)
	}
	files, err := Embedded(version)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return WriteTree(files, dir)
}
