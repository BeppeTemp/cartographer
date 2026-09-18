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
// itself or any directory component under the client base dir — is a symlink, or
// anything else that is not a plain file or a plain directory (see
// isUnsafeDestination). The sentinel keeps its symlink name because that is what
// every caller matches with errors.Is and what the incident below was about; the
// refusal it carries is broader than its name.
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
// obvious where the write would have landed. A destination that is refused
// without being a symlink (see isUnsafeDestination) has no target to name, so it
// gets the shape of the refusal instead of a "-> (unreadable)" that reads like a
// broken link.
func symlinkError(path string) error {
	if target, err := os.Readlink(path); err == nil {
		return fmt.Errorf("%w: refusing to write through symlink %s -> %s (destination must be a real path; a dotfile manager or an earlier bootstrap may have linked it)",
			ErrSymlinkDestination, path, target)
	}
	return fmt.Errorf("%w: refusing to write through %s, which is neither a plain file nor a plain directory (destination must be a real path; a reparse point, junction, device or socket may be standing in for it)",
		ErrSymlinkDestination, path)
}

// isUnsafeDestination reports whether path exists and is something provisioning
// must not write through: anything that is not a plain regular file or a plain
// directory. A path that does not exist yet is not one — provisioning creates it.
//
// It is deliberately broader than the symlink D148 was written against, and only
// ever broader: no path refused before is accepted now. The reason is that a
// Windows directory *junction* is a reparse point, and whether Go reports one as
// os.ModeSymlink or only as os.ModeIrregular **was never verified on a Windows
// host** — not while the plan was written and not while this was implemented.
// Refusing both, plus devices, sockets and named pipes, makes the guard correct
// either way instead of correct only if the guess was right. The cost is that a
// deliberate FIFO destination is now refused too, which no provisioning
// destination has any business being.
//
// A regular file where a directory is expected stays *accepted* here on purpose:
// MkdirAll already fails on it with an accurate error, and reporting it as a
// hostile destination would be a false accusation.
func isUnsafeDestination(path string) bool {
	info, err := lstat(path)
	if err != nil {
		return false
	}
	mode := info.Mode()
	return !mode.IsRegular() && !mode.IsDir()
}

// lstat is os.Lstat behind a test seam: os.ModeIrregular is the mode this guard
// exists for and the one no test host can create on demand.
var lstat = os.Lstat

// ensureSafeDir walks relDir's components under baseDir and refuses the first
// one isUnsafeDestination rejects. baseDir itself is deliberately exempt: it may
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
		if isUnsafeDestination(current) {
			return symlinkError(current)
		}
	}
	return nil
}

// writeFileNoFollow is os.WriteFile that refuses to follow a symlinked — or
// otherwise non-plain — target.
func writeFileNoFollow(path string, data []byte, perm os.FileMode) error {
	if isUnsafeDestination(path) {
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
