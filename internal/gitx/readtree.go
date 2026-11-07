package gitx

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/procattr"
)

// ErrGitMissing reports that git is not on PATH.
var ErrGitMissing = errors.New("installing a plugin from a git URL needs git on PATH; a local folder does not")

// TreeEntry is a regular file of a commit's tree, read from the object database.
type TreeEntry struct {
	Path       string // slash form, relative to the repository root
	Content    []byte
	Executable bool // mode 100755
}

// TreeLimits bound what FetchTree reads. Skip names the paths the caller never
// wants: they are not counted and their blobs are not read.
type TreeLimits struct {
	MaxEntries  int
	MaxFileSize int
	MaxTotal    int // bytes in all the files kept; 0 means no cap
	Skip        func(path string) bool
}

// FetchTree fetches ref of remote (a branch, a tag or a full commit sha; empty
// means HEAD) at depth 1 into a throwaway repository under tmpParent and
// returns the files of the commit's tree with the commit's 40-hex id.
//
// The tree is read from the object database and never checked out, so the
// bytes returned are the committed ones whatever core.autocrlf,
// .gitattributes (eol, filter, export-subst) or the host OS would have done to
// a working copy. Credentials are the machine's own, as for `kb clone`. A
// symlink, a gitlink, a blob over MaxFileSize or more than MaxEntries entries
// is refused naming the path, before any blob is read; so is a listing whose
// blobs add up to more than MaxTotal (0: no cap). The fetch runs under the
// caller's deadline, else FetchTimeout.
func FetchTree(ctx context.Context, remote, ref, tmpParent string, lim TreeLimits) ([]TreeEntry, string, error) {
	if err := ValidateRemote(remote); err != nil {
		return nil, "", err
	}
	if strings.HasPrefix(ref, "-") {
		return nil, "", errors.New("ref must not start with '-'")
	}
	if ref == "" {
		ref = "HEAD"
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, "", ErrGitMissing
	}
	if err := os.MkdirAll(tmpParent, 0o700); err != nil {
		return nil, "", err
	}
	tmp, err := os.MkdirTemp(tmpParent, ".tmp-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(tmp)

	env := cloneEnv(remote, nil)
	local := func(args ...string) *exec.Cmd {
		cmd := procattr.Hide(exec.CommandContext(ctx, "git", append([]string{"-C", tmp, "-c", "core.autocrlf=false"}, args...)...))
		cmd.Env = env
		return cmd
	}
	if out, err := local("init", "-q").CombinedOutput(); err != nil {
		return nil, "", fmt.Errorf("git init: %w: %s", err, strings.TrimSpace(string(out)))
	}
	// The fetch is the call that meets an unreachable remote: it runs under
	// the caller's deadline, else FetchTimeout, as the other network calls do.
	fctx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		fctx, cancel = context.WithTimeout(ctx, FetchTimeout)
		defer cancel()
	}
	fetch := netCommand(fctx, env, "-C", tmp, "-c", "core.autocrlf=false", "fetch", "-q", "--depth", "1",
		"--no-tags", "--no-recurse-submodules", "--", remote, ref)
	if out, err := fetch.CombinedOutput(); err != nil {
		// Names the source and the ref, not the throwaway directory.
		return nil, "", gitNetError(fctx, remote, "fetch", ref, err, strings.ReplaceAll(string(out), tmp, "<temp>"))
	}
	out, err := local("rev-parse", "--verify", "FETCH_HEAD^{commit}").Output()
	sha := strings.TrimSpace(string(out))
	if err != nil || len(sha) != 40 {
		return nil, "", fmt.Errorf("git rev-parse FETCH_HEAD in %s: no commit", RedactRemoteURL(remote))
	}
	type item struct {
		path, oid string
		exec      bool
	}
	var items []item
	total := 0
	ls := local("ls-tree", "-r", "-z", "-l", sha)
	lsOut, err := ls.StdoutPipe()
	if err != nil {
		return nil, "", err
	}
	if err := ls.Start(); err != nil {
		return nil, "", fmt.Errorf("git ls-tree: %w", err)
	}
	// The listing is read as it is produced, so a refusal stops git at once.
	refuse := func(format string, a ...any) ([]TreeEntry, string, error) {
		_ = ls.Process.Kill()
		_ = ls.Wait()
		return nil, "", fmt.Errorf(format, a...)
	}
	lr := bufio.NewReader(lsOut)
	for {
		rec, rerr := lr.ReadString(0)
		if rerr != nil && rerr != io.EOF {
			return refuse("git ls-tree: %w", rerr)
		}
		rec = strings.TrimSuffix(rec, "\x00")
		if rec == "" {
			if rerr == io.EOF {
				break
			}
			continue
		}
		meta, path, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta) // mode, type, object, size ("-" for a gitlink)
		if !ok || len(f) != 4 {
			return refuse("git ls-tree: unreadable entry %q", rec)
		}
		if lim.Skip != nil && lim.Skip(path) {
			continue
		}
		switch f[0] {
		case "100644", "100755":
		case "120000":
			return refuse("%s: a symlink", path)
		case "160000":
			return refuse("%s: a submodule", path)
		default:
			return refuse("%s: mode %s is not a regular file", path, f[0])
		}
		n, _ := strconv.Atoi(f[3])
		if n > lim.MaxFileSize {
			return refuse("%s: %d bytes, over the %d cap of a file", path, n, lim.MaxFileSize)
		}
		total += n
		if lim.MaxTotal > 0 && total > lim.MaxTotal {
			return refuse("%s: more than %d bytes in all", path, lim.MaxTotal)
		}
		items = append(items, item{path, f[2], f[0] == "100755"})
		if len(items) > lim.MaxEntries {
			return refuse("more than %d files", lim.MaxEntries)
		}
		if rerr == io.EOF {
			break
		}
	}
	if err := ls.Wait(); err != nil {
		return nil, "", fmt.Errorf("git ls-tree: %w", err)
	}

	var ids bytes.Buffer
	for _, it := range items {
		ids.WriteString(it.oid + "\n")
	}
	cat := local("cat-file", "--batch")
	cat.Stdin = &ids
	pipe, err := cat.StdoutPipe()
	if err != nil {
		return nil, "", err
	}
	if err := cat.Start(); err != nil {
		return nil, "", fmt.Errorf("git cat-file: %w", err)
	}
	r := bufio.NewReader(pipe)
	entries := make([]TreeEntry, 0, len(items))
	for _, it := range items {
		header, err := r.ReadString('\n')
		if err != nil {
			_ = cat.Wait()
			return nil, "", fmt.Errorf("git cat-file: %s: %w", it.path, err)
		}
		hf := strings.Fields(header)
		size, convErr := 0, error(nil)
		if len(hf) == 3 && hf[1] == "blob" {
			size, convErr = strconv.Atoi(hf[2])
		}
		if len(hf) != 3 || convErr != nil || size > lim.MaxFileSize {
			_ = cat.Process.Kill()
			_ = cat.Wait()
			return nil, "", fmt.Errorf("%s: unreadable or oversized blob (%s)", it.path, strings.TrimSpace(header))
		}
		buf := make([]byte, size+1) // the blob and the newline that follows it
		if _, err := io.ReadFull(r, buf); err != nil {
			_ = cat.Wait()
			return nil, "", fmt.Errorf("git cat-file: %s: %w", it.path, err)
		}
		entries = append(entries, TreeEntry{Path: it.path, Content: buf[:size], Executable: it.exec})
	}
	if err := cat.Wait(); err != nil {
		return nil, "", fmt.Errorf("git cat-file: %w", err)
	}
	return entries, sha, nil
}
