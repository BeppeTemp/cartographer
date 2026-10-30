package recipe

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// CloneFunc clones url into dest (an existing empty directory).
type CloneFunc func(ctx context.Context, url, dest string) error

// cloneTimeout bounds one clone: a feed's repository is read by an unattended
// worker, and a slow or endless server must not hold the poll.
const cloneTimeout = 5 * time.Minute

// defaultClone is an anonymous https clone (gitx.AnonymousEnv: no operator
// credential, no config, no redirect) without history blobs, bounded in time.
func defaultClone(ctx context.Context, url, dest string) error {
	ctx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()
	return gitx.CloneAnonymous(ctx, url, dest)
}

const (
	defaultInclude = "docs/**/*.md"
	maxCursor      = 1024
)

var shaRE = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// gitDocs lists the files of a git repository that changed between two commits.
type gitDocs struct {
	text  string
	hosts []string
	clone CloneFunc
}

func newGitDocs(text string, o Options) *gitDocs {
	c := o.Clone
	if c == nil {
		c = defaultClone
	}
	return &gitDocs{text: text, hosts: o.GitHosts, clone: c}
}

func (g *gitDocs) Name() string         { return "git-docs" }
func (g *gitDocs) Shape() string        { return ShapeFeed }
func (g *gitDocs) Instructions() string { return g.text }

// cursor is the parsed feed_cursor: "" (first poll), a bare commit (listed up to
// there) or "<from>..<to>@<path>" (from -> to in progress; entries up to path, in
// byte order, are done; from is empty on the first poll).
type cursor struct{ from, to, after string }

func parseCursor(c string) (cursor, bool) {
	switch {
	case c == "":
		return cursor{}, true
	case shaRE.MatchString(c):
		return cursor{from: c}, true
	}
	span, after, ok := strings.Cut(c, "@")
	from, to, ok2 := strings.Cut(span, "..")
	if !ok || !ok2 || after == "" || !shaRE.MatchString(to) || (from != "" && !shaRE.MatchString(from)) {
		return cursor{}, false
	}
	return cursor{from: from, to: to, after: after}, true
}

func (g *gitDocs) List(ctx context.Context, req Request) (Listing, error) {
	u, err := httpsRemote(req.Locator, g.hosts) // before any clone
	if err != nil {
		return Listing{}, err
	}
	cur, ok := parseCursor(req.Cursor)
	if !ok {
		return Listing{}, fail(CodeCursorInvalid, "feed_cursor is not a commit or a listing in progress; clear feed_cursor to list everything again")
	}
	tmp, err := os.MkdirTemp("", "cartographer-git-docs-*")
	if err != nil {
		return Listing{}, fail(CodeListFailed, "no temporary directory")
	}
	defer os.RemoveAll(tmp) // losing the clone is harmless: the cursor is in the Source
	dir := filepath.Join(tmp, "repo")
	// Every git command run in the clone is anonymous too, with the operator's
	// HOME out of reach (gitx.AnonymousReadEnv): the clone's own config is a
	// stranger's, the operator's must not be read next to it.
	home := filepath.Join(tmp, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		return Listing{}, fail(CodeListFailed, "no temporary directory")
	}
	env := gitx.AnonymousReadEnv(home)
	if err := g.clone(ctx, u.String(), dir); err != nil {
		return Listing{}, fail(CodeListFailed, "clone failed: %s", oneLine(err.Error()))
	}
	head, err := gitx.HeadSHA(dir, env...)
	if err != nil {
		return Listing{}, fail(CodeListFailed, "the repository has no HEAD")
	}
	to := cur.to
	if to == "" {
		to = head
	}
	// The cursor is never moved backwards: a commit that is not an ancestor of
	// HEAD (or no longer exists) means history was rewritten.
	invalid := fail(CodeCursorInvalid, "the cursor is not an ancestor of HEAD (history rewritten?); clear feed_cursor to list everything again")
	if cur.to != "" {
		if anc, err := gitx.IsAncestor(dir, cur.to, head, env...); err != nil || !anc {
			return Listing{}, invalid
		}
	}
	if cur.from != "" {
		if anc, err := gitx.IsAncestor(dir, cur.from, to, env...); err != nil || !anc {
			return Listing{}, invalid
		}
	}
	include := req.Include
	if len(include) == 0 {
		include = []string{defaultInclude}
	}
	all, err := changes(dir, cur.from, to, env)
	if err != nil {
		return Listing{}, fail(CodeListFailed, "%s", oneLine(err.Error()))
	}
	var entries []Entry
	for _, c := range all {
		if c.Key > cur.after || cur.after == "" {
			if matchAny(include, c.Key) {
				c.Locator = u.String() + "#" + c.Key
				entries = append(entries, c)
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	max := req.MaxEntries
	if max <= 0 {
		max = DefaultMaxEntries
	}
	next := to
	if len(entries) > max {
		entries = entries[:max]
		next = cur.from + ".." + to + "@" + entries[max-1].Key
		if len(next) > maxCursor {
			return Listing{}, fail(CodeListFailed, "a path is too long for the cursor")
		}
	}
	return Listing{Entries: entries, NextCursor: next}, nil
}

// changes lists the files from -> to as entries (Key = Path is set here; the
// Locator is the caller's). from empty = everything at to is added.
func changes(dir, from, to string, env []string) ([]Entry, error) {
	var out []Entry
	if from == "" {
		blobs, err := gitx.TreeBlobs(dir, to, env...)
		if err != nil {
			return nil, err
		}
		for _, b := range blobs {
			if documentMode(b.Mode) {
				out = append(out, Entry{Key: b.Path, Title: b.Path, Revision: b.ID, Change: Added})
			}
		}
		return out, nil
	}
	diff, err := gitx.DiffTree(dir, from, to, env...)
	if err != nil {
		return nil, err
	}
	for _, d := range diff {
		if d.Status != "D" && !documentMode(d.Mode) {
			continue // became a symlink or a submodule: not a document
		}
		e := Entry{Key: d.Path, Title: d.Path, Revision: d.ID}
		switch d.Status {
		case "A":
			e.Change = Added
		case "D":
			e.Change = Removed
		default:
			e.Change = Modified
		}
		out = append(out, e)
	}
	return out, nil
}

// documentMode: a regular file. A symlink (120000) or a submodule (160000) is not
// a document, and the executor would refuse it on every poll.
func documentMode(m string) bool { return m == "100644" || m == "100755" }

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
