package clientconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// TokenFileName is the private copy of the bearer token (D699), next to
// FileName. Launchd, systemd user units and the Task Scheduler do not inherit a
// login shell's exports, so an unattended sync or doctor run cannot read
// `token_env`; the file is what they read instead.
const TokenFileName = ".cartographer-token"

// TokenPath returns the full path to the token file inside dir.
func TokenPath(dir string) string {
	return filepath.Join(dir, TokenFileName)
}

// WriteToken stores tok in dir's token file with mode 0600, atomically (temp
// file in the same directory, then rename). It refuses a token path that is a
// symlink or any non-regular file: the token must never be written through a
// link to somewhere else. On Windows the mode bits mean nothing; the file sits
// in the user's profile, whose default ACL already restricts it.
func WriteToken(dir, tok string) error {
	if strings.TrimSpace(tok) == "" {
		return errors.New("clientconfig: empty token")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("clientconfig: mkdir %s: %w", dir, err)
	}
	path := TokenPath(dir)
	if fi, err := os.Lstat(path); err == nil {
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("clientconfig: %s is not a regular file (symlink?): refusing to write the token through it", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clientconfig: stat %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, TokenFileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("clientconfig: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		tmp.Close()
		return fmt.Errorf("clientconfig: chmod temp: %w", err)
	}
	if _, err := tmp.WriteString(tok + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("clientconfig: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("clientconfig: close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("clientconfig: replace %s: %w", path, err)
	}
	return nil
}

// ReadToken returns the token stored in dir, or ("", nil) when there is none.
// On POSIX it refuses a file readable by group or others, and a symlink, with
// an error naming the fix: a token other users can read is already leaked, and
// a silent fallback would hide that.
func ReadToken(dir string) (string, error) {
	path := TokenPath(dir)
	fi, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("clientconfig: stat %s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("clientconfig: %s is not a regular file (symlink?): refusing to read it", path)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("clientconfig: %s is readable by other users (mode %04o): run `chmod 600 %s`", path, fi.Mode().Perm(), path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("clientconfig: read %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// RemoveToken deletes dir's token file; a missing file is not an error.
func RemoveToken(dir string) error {
	if err := os.Remove(TokenPath(dir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clientconfig: remove %s: %w", TokenPath(dir), err)
	}
	return nil
}
