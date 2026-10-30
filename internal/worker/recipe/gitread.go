package recipe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// GitReader reads one file of a git entry for the unit executor: the locator is
// "<repo URL>#<path>" as git-docs writes it. It clones a repository once per
// reader, into a temporary directory that Close deletes, and reads the file as
// of the clone, which may be newer than the unit's revision: a document that
// changed again is listed as modified by the next poll and gets its own unit.
type GitReader struct {
	hosts []string
	clone CloneFunc

	mu    sync.Mutex
	root  string
	repos map[string]string // URL -> clone dir
}

// NewGitReader reads from the hosts of git.allow_hosts only. clone nil = https.
func NewGitReader(hosts []string, clone CloneFunc) *GitReader {
	if clone == nil {
		clone = defaultClone
	}
	return &GitReader{hosts: hosts, clone: clone, repos: map[string]string{}}
}

// SplitGitLocator cuts "<repo URL>#<path>" at the last "#" and checks the host
// and the path: a repository outside git.allow_hosts, a path that is not local
// (absolute or with "..") and a non-https remote are refused.
func SplitGitLocator(locator string, hosts []string) (repo, path string, err error) {
	i := strings.LastIndex(locator, "#")
	if i < 0 {
		return "", "", errors.New("the locator has no #path")
	}
	path = locator[i+1:]
	if path == "" || !filepath.IsLocal(filepath.FromSlash(path)) || strings.Contains(path, "\\") {
		return "", "", errors.New("the path in the locator is not inside the repository")
	}
	u, err := httpsRemote(locator[:i], hosts)
	if err != nil {
		return "", "", err
	}
	return u.String(), path, nil
}

// Read returns the file, cut at max bytes; cut reports a truncation.
func (r *GitReader) Read(ctx context.Context, locator string, max int) (text string, cut bool, err error) {
	repo, rel, err := SplitGitLocator(locator, r.hosts)
	if err != nil {
		return "", false, err
	}
	dir, err := r.repoDir(ctx, repo)
	if err != nil {
		return "", false, err
	}
	// Every component is lstat'ed: a symlink anywhere in the path (a directory
	// too) could point outside the clone.
	p := dir
	for _, seg := range strings.Split(rel, "/") {
		p = filepath.Join(p, seg)
		fi, err := os.Lstat(p)
		if err != nil {
			return "", false, errors.New("the file is not in the repository")
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", false, errors.New("the path goes through a symbolic link")
		}
	}
	f, err := os.Open(p)
	if err != nil {
		return "", false, errors.New("the file cannot be read")
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return "", false, errors.New("the path is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return "", false, errors.New("the file cannot be read")
	}
	if cut = len(data) > max; cut {
		data = data[:max]
	}
	return string(data), cut, nil
}

func (r *GitReader) repoDir(ctx context.Context, repo string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d, ok := r.repos[repo]; ok {
		return d, nil
	}
	if r.root == "" {
		root, err := os.MkdirTemp("", "cartographer-git-read-*")
		if err != nil {
			return "", errors.New("no temporary directory")
		}
		r.root = root
	}
	dir := filepath.Join(r.root, fmt.Sprintf("r%d", len(r.repos)))
	if err := r.clone(ctx, repo, dir); err != nil {
		return "", fmt.Errorf("clone failed: %s", oneLine(err.Error()))
	}
	r.repos[repo] = dir
	return dir, nil
}

// Close deletes the clones.
func (r *GitReader) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.root != "" {
		_ = os.RemoveAll(r.root)
		r.root, r.repos = "", map[string]string{}
	}
}
