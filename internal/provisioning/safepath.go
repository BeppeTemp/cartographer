package provisioning

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// refuse records a symlink refusal against one artifact and reports whether the
// caller should skip it and carry on. Anything else is a real error and stays
// fatal to the pass. One symlinked skill directory must not abort a whole sync —
// same treatment Apply already gives an unsupported kind — and the artifact is
// deliberately left out of Written and out of the lockfile, so the next sync
// retries and reports again.
func refuse(result *AppliedResult, a Artifact, err error) bool {
	if !errors.Is(err, ErrSymlinkDestination) {
		return false
	}
	result.Refused = append(result.Refused, a)
	result.Warnings = append(result.Warnings, fmt.Sprintf("%s/%s not installed: %v", a.Kind, a.Name, err))
	return true
}

// ErrSymlinkDestination is returned when a provisioning destination — the file
// itself or any directory component under the client base dir — is a symlink.
//
// os.WriteFile on a symlinked path opens the *target* with O_WRONLY|O_TRUNC: it
// does not replace the link. Symlinked client-config directories are ordinary
// (a dotfile manager, a monorepo checkout, a shared team directory, an earlier
// bootstrap that linked skills out of a source repository), and when the target
// is another git repository the write lands there — observed in the field as 23
// modified files in a repository that had to stay untouched, each stamped with a
// provenance footer declaring a false origin.
//
// The KB side already guards this (internal/kb/asset.go Lstat's every component
// and refuses to traverse a link); the client side did not, hence D148.
var ErrSymlinkDestination = errors.New("provisioning: destination is a symlink")

// symlinkError builds the operator-facing refusal, naming the target so it is
// obvious where the write would have landed.
func symlinkError(path string) error {
	target, err := os.Readlink(path)
	if err != nil {
		target = "(unreadable)"
	}
	return fmt.Errorf("%w: refusing to write through symlink %s -> %s (destination must be a real path; a dotfile manager or an earlier bootstrap may have linked it)",
		ErrSymlinkDestination, path, target)
}

// isSymlink reports whether path exists and is a symlink. A path that does not
// exist yet is not one: provisioning creates it.
func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

// ensureSafeDir walks relDir's components under baseDir and refuses the first
// one that is a symlink. baseDir itself is deliberately exempt: it may
// legitimately be a link (a symlinked $HOME, or a provider root resolved from
// BaseDirEnv), so the walk starts below it. A component that does not exist yet
// is fine.
func ensureSafeDir(baseDir, relDir string) error {
	relDir = filepath.Clean(relDir)
	if relDir == "." || relDir == string(filepath.Separator) {
		return nil
	}
	current := baseDir
	for _, part := range strings.Split(relDir, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		if isSymlink(current) {
			return symlinkError(current)
		}
	}
	return nil
}

// writeFileNoFollow is os.WriteFile that refuses to follow a symlinked target.
func writeFileNoFollow(path string, data []byte, perm os.FileMode) error {
	if isSymlink(path) {
		return symlinkError(path)
	}
	return os.WriteFile(path, data, perm)
}

// mkdirAllNoFollow is os.MkdirAll with the component walk in front of it, so a
// symlinked intermediate directory is refused instead of silently traversed.
// baseDir anchors the walk; dir must be inside it.
func mkdirAllNoFollow(baseDir, dir string, perm os.FileMode) error {
	rel, err := filepath.Rel(baseDir, dir)
	if err == nil && !strings.HasPrefix(rel, "..") {
		if err := ensureSafeDir(baseDir, rel); err != nil {
			return err
		}
	}
	return os.MkdirAll(dir, perm)
}
