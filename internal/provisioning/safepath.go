package provisioning

import (
	"errors"
	"fmt"
	"os"

	"github.com/BeppeTemp/cartographer/internal/safewrite"
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

// ErrSymlinkDestination is the refusal internal/safewrite returns for a
// destination — the file itself or any directory component under the client base
// dir — that is a symlink or anything else that is not a plain file or a plain
// directory. It is the same error value, so errors.Is matches on either name
// (D148).
var ErrSymlinkDestination = safewrite.ErrSymlinkDestination

// The guard itself lives in internal/safewrite, which the configurator and
// blocktext packages also need and cannot take from here (this package imports
// them). These are the names the rest of the package has always used.

func isUnsafeDestination(path string) bool { return safewrite.IsUnsafe(path) }

func ensureSafeDir(baseDir, relDir string) error { return safewrite.EnsureSafeDir(baseDir, relDir) }

func writeFileNoFollow(path string, data []byte, perm os.FileMode) error {
	return safewrite.WriteFileNoFollow(path, data, perm)
}

func mkdirAllNoFollow(baseDir, dir string, perm os.FileMode) error {
	return safewrite.MkdirAllNoFollow(baseDir, dir, perm)
}

// refusePrune is refuse for the removal side: a managed file that could not be
// pruned because it, or its registration, sits behind a symlink. It is left out
// of the pruned list and kept in the lockfile by the caller, so the next sync
// retries and reports again; one artifact is reported once however many of its
// files were refused.
func refusePrune(result *AppliedResult, mf ManagedFile, err error) {
	for _, a := range result.Refused {
		if a.Kind == mf.Kind && a.Name == mf.Name {
			return
		}
	}
	result.Refused = append(result.Refused, Artifact{Kind: mf.Kind, Name: mf.Name, Source: mf.Source})
	result.Warnings = append(result.Warnings, fmt.Sprintf("%s/%s not removed: %v", mf.Kind, mf.Name, err))
}
