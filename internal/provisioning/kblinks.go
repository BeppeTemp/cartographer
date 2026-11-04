package provisioning

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A KB comes from a remote and git stores symlinks, so the tree of a clone is
// not under the control of the machine that serves it. An artifact built from
// it is made of regular files of that tree: the manifest builder and the file
// reader never read through a symlink, whether it is the file itself, a
// directory above it or the kind's own directory (skills/, hooks/, agents/,
// mcp/). The artifact that holds one is left out of the manifest with an issue
// that names it; the artifacts around it are unaffected. The refusal is the
// same one the artifact tools and the concept paths apply (D148, D1025).
//
// It is a server-side rule about a KB's tree. The client-side hash of what it
// materialized (ContentHashDirOSForKind, VerifyManaged) is a different
// question and does not apply it.

// kbSymlinkError is returned where an artifact's file is a symlink.
type kbSymlinkError struct{ rel string }

func (e *kbSymlinkError) Error() string {
	return fmt.Sprintf("symlink %q: an artifact is made of regular files and is not read through a link", e.rel)
}

// firstKBLink returns the first component of rel (slash-separated, below
// kbRoot) that is a symlink, as a slash path from kbRoot, or "" when none is.
// A component that does not exist ends the walk: there is nothing to read
// below it. kbRoot itself may be a link (a mount) and is not inspected.
func firstKBLink(kbRoot, rel string) (string, error) {
	parts := strings.Split(rel, "/")
	cur := kbRoot
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return strings.Join(parts[:i+1], "/"), nil
		}
	}
	return "", nil
}

// kbLinkIssue is the manifest issue for an artifact left out because of a link.
func kbLinkIssue(kbName, what, link string) string {
	return fmt.Sprintf("KB %q: %s not served: symlink at %q (an artifact is made of regular files)", kbName, what, link)
}
