package worker

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// maxInstructionsBytes caps a prompt task's instructions file: the text rides in
// the system prompt of every step of the loop (30 by default), so a larger file
// would spend the task's whole grant on repeating itself.
const maxInstructionsBytes = 16 << 10

// loadInstructions reads a prompt task's instructions from the config directory
// and nowhere else (D15). The file is opened through os.Root, which refuses a
// name that leaves dir (absolute, "..", or a symlink that escapes) and follows a
// relative link inside it: Kubernetes ConfigMap volumes expose every file that
// way. No error echoes the file's content.
func loadInstructions(dir, rel string) (string, error) {
	switch {
	case dir == "":
		return "", errors.New("instructions_file needs a config loaded from a file: there is no directory to resolve it in")
	case rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) || filepath.VolumeName(rel) != "":
		return "", fmt.Errorf("instructions_file %q must be a relative path inside the config directory", rel)
	case !filepath.IsLocal(rel):
		return "", fmt.Errorf("instructions_file %q must stay inside the config directory (no \"..\" segment)", rel)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", fmt.Errorf("instructions_file %q: the config directory cannot be opened", rel)
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		return "", fmt.Errorf("instructions_file %q: %s", rel, errReason(err))
	}
	defer f.Close()
	st, err := f.Stat()
	switch {
	case err != nil:
		return "", fmt.Errorf("instructions_file %q: %s", rel, errReason(err))
	case !st.Mode().IsRegular():
		return "", fmt.Errorf("instructions_file %q is not a regular file", rel)
	case st.Size() > maxInstructionsBytes:
		return "", fmt.Errorf("instructions_file %q is %d bytes, over the limit of %d", rel, st.Size(), maxInstructionsBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxInstructionsBytes+1))
	switch {
	case err != nil:
		return "", fmt.Errorf("instructions_file %q: %s", rel, errReason(err))
	case len(data) > maxInstructionsBytes:
		return "", fmt.Errorf("instructions_file %q is over the limit of %d bytes", rel, maxInstructionsBytes)
	case len(bytes.TrimSpace(data)) == 0:
		return "", fmt.Errorf("instructions_file %q is empty", rel)
	case !utf8.Valid(data):
		return "", fmt.Errorf("instructions_file %q is not valid UTF-8", rel)
	case bytes.IndexByte(data, 0) >= 0:
		return "", fmt.Errorf("instructions_file %q contains a NUL byte", rel)
	}
	return strings.TrimSpace(string(data)), nil
}

// errReason names why an open failed without the OS error's path or text.
func errReason(err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "no such file in the config directory (a symlink may also leave it)"
	case errors.Is(err, os.ErrPermission):
		return "permission denied"
	}
	return "cannot be read (a symlink that leaves the config directory is refused)"
}
