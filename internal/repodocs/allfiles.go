package repodocs

import (
	"os"
	"path/filepath"
	"sort"
)

// AllFiles walks the repository the way TextFiles does (same skipped
// directories, regular files only, slash paths relative to root, sorted) but
// without its extension filter: the doc-owner map names a Makefile and shell
// scripts, which no reference gate reads.
func AllFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			if rel != "." && skipDir(rel, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
