package mcpserver

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// relPathErr is err's text with the absolute paths of the host filesystem
// reduced to what a client of the KB already knows: a path below one of roots
// is shown relative to it, any other absolute path by its last element. Use
// it, not %v of the raw error, wherever an *fs.PathError or *os.LinkError can
// reach a tool result: a client of a shared server addresses concepts by id,
// and the directory the server keeps its data in is not part of that contract.
func relPathErr(err error, roots ...string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	var pe *fs.PathError
	if errors.As(err, &pe) {
		msg = strings.ReplaceAll(msg, pe.Path, relToRoots(pe.Path, roots))
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		msg = strings.ReplaceAll(msg, le.Old, relToRoots(le.Old, roots))
		msg = strings.ReplaceAll(msg, le.New, relToRoots(le.New, roots))
	}
	return scrubRoots(msg, roots...)
}

// relToRoots is p relative to the first root that contains it, p's last
// element when it is absolute and in none, and p itself otherwise.
func relToRoots(p string, roots []string) string {
	if !filepath.IsAbs(p) {
		return p
	}
	for _, r := range roots {
		if r == "" {
			continue
		}
		if rel, err := filepath.Rel(r, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(p)
}

// scrubRoots removes every occurrence of the roots (and of their symlink-
// resolved forms) from text, longest first, so that "<root>/a/b" becomes
// "a/b" and a bare "<root>" becomes ".".
func scrubRoots(text string, roots ...string) string {
	var all []string
	for _, r := range roots {
		if r == "" {
			continue
		}
		all = append(all, r)
		if res, err := filepath.EvalSymlinks(r); err == nil && res != r {
			all = append(all, res)
		}
	}
	sort.Slice(all, func(i, j int) bool { return len(all[i]) > len(all[j]) })
	for _, r := range all {
		text = strings.ReplaceAll(text, r+string(filepath.Separator), "")
		text = strings.ReplaceAll(text, r, ".")
	}
	return text
}
