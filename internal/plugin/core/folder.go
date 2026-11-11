package core

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/execbit"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// ReadFolder walks root with Lstat: a symlink or any non-regular file is
// returned as a Special entry, for ReadTree to list among the findings (a stored
// tree holding one is ErrModified, see specialIn), and the entries ReadTree
// ignores are not read.
func ReadFolder(root string) ([]TreeFile, error) {
	var files []TreeFile
	total := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		first, _, _ := strings.Cut(rel, "/")
		if Ignored(first) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			// Not an error here: ReadTree reports it in the list of tree findings.
			files = append(files, TreeFile{Path: rel, Special: true})
			return nil
		}
		if fi.Size() > int64(contract.ArtifactMaxFileSize) {
			return fmt.Errorf("%s: %d bytes, over the %d cap of a file", rel, fi.Size(), contract.ArtifactMaxFileSize)
		}
		if len(files) >= MaxFiles {
			return fmt.Errorf("more than %d files", MaxFiles)
		}
		// Lstat's size can change before the read: read at most one byte past
		// the cap of a file.
		fh, err := os.Open(p)
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(fh, int64(contract.ArtifactMaxFileSize)+1))
		fh.Close()
		if err != nil {
			return err
		}
		if len(b) > contract.ArtifactMaxFileSize {
			return fmt.Errorf("%s: over the %d cap of a file", rel, contract.ArtifactMaxFileSize)
		}
		if total += len(b); total > MaxBytes {
			return fmt.Errorf("%s: more than %d bytes in all", rel, MaxBytes)
		}
		files = append(files, TreeFile{Path: rel, Content: b, Executable: execbit.IsExecutable(fi.Mode())})
		return nil
	})
	return files, err
}
