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

// relToRoots is p relative to the first root that contains it (the root as
// given or with its symlinks resolved), p's last element when it is absolute
// and in none, and p itself otherwise.
func relToRoots(p string, roots []string) string {
	if !filepath.IsAbs(p) {
		return p
	}
	for _, r := range rootForms(roots) {
		if rel, err := filepath.Rel(r, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(p)
}

// rootForms lists every root and its symlink-resolved form, longest first.
func rootForms(roots []string) []string {
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
	return all
}

// scrubRoots removes every occurrence of the roots (and of their symlink-
// resolved forms) from text, longest first, so that "<root>/a/b" becomes
// "a/b" and a bare "<root>" becomes ".". An occurrence counts only at a path
// boundary: it must not continue a longer path on either side ("/data/kb2/x"
// and "/var/data/kb/x" are other paths than "/data/kb").
func scrubRoots(text string, roots ...string) string {
	for _, r := range rootForms(roots) {
		text = replaceAtBoundary(text, r)
	}
	return text
}

func isPathChar(b byte) bool {
	return b == '/' || b == '\\' || b == '.' || b == '_' || b == '-' || b == '~' ||
		b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= 0x80
}

func replaceAtBoundary(text, root string) string {
	var b strings.Builder
	for {
		i := strings.Index(text, root)
		if i < 0 {
			break
		}
		end := i + len(root)
		before := i == 0 || !isPathChar(text[i-1])
		switch {
		case before && end < len(text) && (text[end] == '/' || text[end] == filepath.Separator):
			b.WriteString(text[:i])
			text = text[end+1:]
		case before && (end == len(text) || !isPathChar(text[end])):
			b.WriteString(text[:i])
			b.WriteByte('.')
			text = text[end:]
		default:
			b.WriteString(text[:end])
			text = text[end:]
		}
	}
	b.WriteString(text)
	return b.String()
}
