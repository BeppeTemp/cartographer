package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/execbit"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// Source is where a plugin comes from: a git remote at a ref, or a folder.
type Source struct {
	Git    bool
	Remote string // git: the remote as typed, without the @ref
	Ref    string // git: branch, tag or full sha; empty means HEAD
	Path   string // folder: absolute, symlinks resolved
}

// String is the form recorded in the user's file.
func (s Source) String() string {
	if !s.Git {
		return s.Path
	}
	if s.Ref == "" {
		return s.Remote
	}
	return s.Remote + "@" + s.Ref
}

// ParseSource reads a source typed by the user (os.Args or their config, never
// a KB or a plugin). An existing directory is a folder, whatever it is called.
// Otherwise it is a git source: a URL with a scheme git accepts or an scp-like
// remote (host:path), optionally followed by @<ref>. The ref is split at the
// LAST @, and only when it lies after the URL's host (or the ':' of an
// scp-like remote) and the text after it holds neither ':' nor '/', so
// https://user@host and git@host:team/x.git stay whole and
// git@host:team/x.git@v1 has ref v1.
func ParseSource(s string) (Source, error) {
	if s == "" {
		return Source{}, errors.New("empty plugin source")
	}
	if fi, err := os.Stat(s); err == nil && fi.IsDir() {
		abs, err := filepath.Abs(s)
		if err == nil {
			abs, err = filepath.EvalSymlinks(abs)
		}
		if err != nil {
			return Source{}, err
		}
		return Source{Path: abs}, nil
	}
	remote, ref := s, ""
	if i := strings.LastIndex(s, "@"); i > refFloor(s) {
		if tail := s[i+1:]; !strings.ContainsAny(tail, ":/") {
			remote, ref = s[:i], tail
			if ref == "" {
				return Source{}, fmt.Errorf("%q: empty ref after '@'", s)
			}
		}
	}
	if err := gitx.ValidateRemote(remote); err != nil {
		return Source{}, err
	}
	if !remoteLike(remote) {
		return Source{}, fmt.Errorf("%q is neither a folder nor a git remote (URL or host:path)", s)
	}
	return Source{Git: true, Remote: remote, Ref: ref}, nil
}

// refFloor is the index an '@' must lie beyond to start a ref: past the
// authority of a URL (so https://user@host is whole) and past the ':' of an
// scp-like remote (so the user@ of git@host:path is not a ref).
func refFloor(s string) int {
	if _, rest, ok := strings.Cut(s, "://"); ok {
		start := len(s) - len(rest)
		if j := strings.Index(rest, "/"); j >= 0 {
			return start + j
		}
		return len(s)
	}
	return strings.Index(s, ":")
}

// remoteLike reports a URL with a scheme or an scp-like host:path.
func remoteLike(r string) bool {
	if strings.Contains(r, "://") {
		return true
	}
	host, rest, ok := strings.Cut(r, ":")
	// A one-letter host is a Windows drive, not a remote.
	return ok && rest != "" && len(host) > 1 && !strings.ContainsAny(host, `/\`)
}

// Fetch brings the files of the source into memory; nothing is stored. tmpParent
// holds the throwaway repository of a git fetch. A git source also returns the
// resolved 40-hex commit; a folder is a snapshot and returns "".
func Fetch(ctx context.Context, s Source, tmpParent string) ([]TreeFile, string, error) {
	if !s.Git {
		files, err := readFolder(s.Path)
		return files, "", err
	}
	entries, sha, err := gitx.FetchTree(ctx, s.Remote, s.Ref, tmpParent, gitx.TreeLimits{
		MaxEntries:  MaxFiles,
		MaxFileSize: provisioning.ArtifactMaxFileSize,
		MaxTotal:    MaxBytes,
		Skip:        func(p string) bool { first, _, _ := strings.Cut(p, "/"); return ignored(first) },
	})
	if err != nil {
		return nil, "", err
	}
	files := make([]TreeFile, len(entries))
	for i, e := range entries {
		files[i] = TreeFile{Path: e.Path, Content: e.Content, Executable: e.Executable}
	}
	return files, sha, nil
}

// readFolder walks root with Lstat: a symlink or any non-regular file is an
// error naming it, and the entries ReadTree ignores are not read.
func readFolder(root string) ([]TreeFile, error) {
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
		if ignored(first) {
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
			return fmt.Errorf("%s: a symlink or non-regular file", rel)
		}
		if fi.Size() > int64(provisioning.ArtifactMaxFileSize) {
			return fmt.Errorf("%s: %d bytes, over the %d cap of a file", rel, fi.Size(), provisioning.ArtifactMaxFileSize)
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
		b, err := io.ReadAll(io.LimitReader(fh, int64(provisioning.ArtifactMaxFileSize)+1))
		fh.Close()
		if err != nil {
			return err
		}
		if len(b) > provisioning.ArtifactMaxFileSize {
			return fmt.Errorf("%s: over the %d cap of a file", rel, provisioning.ArtifactMaxFileSize)
		}
		if total += len(b); total > MaxBytes {
			return fmt.Errorf("%s: more than %d bytes in all", rel, MaxBytes)
		}
		files = append(files, TreeFile{Path: rel, Content: b, Executable: execbit.IsExecutable(fi.Mode())})
		return nil
	})
	return files, err
}
