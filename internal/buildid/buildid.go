// Package buildid names the build of the running executable: the VCS revision
// the toolchain embedded, or a hash of the file when none is. `/health`
// reports it as `build` (D2164) so two builds that
// share a version string, a release candidate rebuilt under the same tag, are
// told apart.
package buildid

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime/debug"
	"sync"
)

const (
	vcsPrefix  = "vcs:"
	hashPrefix = "sha256:"
)

var (
	once sync.Once
	self string
)

// Self is the identity of this process's executable, "" when neither a clean
// VCS revision nor a readable file is available. It is computed once, and the
// first call must happen at process start: a server whose file is replaced
// later would otherwise hash the replacement and report the new build for the
// old process, which is exactly the confusion the field exists to remove.
func Self() string {
	once.Do(func() {
		if info, ok := debug.ReadBuildInfo(); ok {
			self = fromInfo(info)
		}
		if self == "" {
			self = hashExecutable()
		}
	})
	return self
}

// fromInfo is "vcs:<revision>" for a build from a clean checkout and "" for
// anything else: a build without VCS stamping (go test, -buildvcs=false) or
// from a modified tree, where the revision no longer says which content was
// built and the file hash does.
func fromInfo(info *debug.BuildInfo) string {
	var rev, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if rev == "" || modified == "true" {
		return ""
	}
	return vcsPrefix + rev
}

func hashExecutable() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hashPrefix + hex.EncodeToString(h.Sum(nil))
}

// Same reports whether two reported identities name one build. An empty one
// (a server that predates the field, or a binary that could not name itself)
// proves nothing, so the comparison falls back to the version alone: Same is
// true. Two non-empty identities must be equal, which includes their kind: a
// revision never equals a hash.
func Same(a, b string) bool {
	return a == "" || b == "" || a == b
}
