package config

import (
	"os"
	"path/filepath"

	"github.com/BeppeTemp/cartographer/internal/settingschema"
	"gopkg.in/yaml.v3"
)

// WriteYAML writes doc to the server config at path the one way every edit of
// that file does it (#1501): encoded with the two-space indent the file's own
// examples use (D177; a file indented differently is normalized to it, its
// comments, key order and values kept), then replaced atomically by WriteFileAtomic.
func WriteYAML(path string, doc *yaml.Node) error {
	out, err := settingschema.Encode(doc)
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, out)
}

// renameFile is os.Rename; a test replaces it to make the last step fail.
var renameFile = os.Rename

// WriteFileAtomic replaces the file at path (the file a symlink points to, when
// it is one) with data through a temporary file in the same directory, keeping
// its permission bits: a crash or a full disk leaves the old file, never a
// truncated one that would turn authentication off. A new file gets 0600.
func WriteFileAtomic(path string, data []byte) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return renameFile(tmp.Name(), path)
}
