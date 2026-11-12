package kb

import (
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// PushLagThreshold is how long a commit may wait for its push before humans'
// surfaces (/health, status, doctor, the tray) report it. A push is debounced
// and retried, so a transient failure that heals within it stays quiet; a push
// whose state is "failed" is reported at once.
const PushLagThreshold = 3 * time.Minute

// PushLag is the part of a KB's git status a human must see: commits that
// are not on the remote and have not left on their own.
type PushLag struct {
	// State is the push state (GitStatus.State) at the time of the reading.
	State string
	// Unpushed is the number of commits not pushed; 0 when the push failed but
	// the comparison with the remote is unknown.
	Unpushed int
	// Error and ErrorNeutral are GitStatus.LastError and LastErrorNeutral, in
	// full: the caller picks the wording for its reader (statusError) and
	// reduces it with Remedy.
	Error, ErrorNeutral string
}

// PushLagFrom decides whether s is worth showing: the state is "failed", or
// commits are unpushed and the oldest of them (oldest, when hasOldest) is at
// least threshold old at now. The age comes from the server's own commit time,
// never from a time a client declared.
func PushLagFrom(s GitStatus, oldest time.Time, hasOldest bool, now time.Time, threshold time.Duration) (PushLag, bool) {
	n := 0
	if s.UnpushedCommits != nil {
		n = *s.UnpushedCommits
	}
	failed := s.State == "failed"
	aged := n > 0 && hasOldest && now.Sub(oldest) >= threshold
	if !failed && !aged {
		return PushLag{}, false
	}
	return PushLag{State: s.State, Unpushed: n, Error: s.LastError, ErrorNeutral: s.LastErrorNeutral}, true
}

// PushLag reads the KB's push state against the remote-tracking ref (no
// network) and reports whether humans should be told.
func (k *KB) PushLag(now time.Time) (PushLag, bool) {
	s := k.GitStatusSnapshot()
	var (
		oldest    time.Time
		hasOldest bool
	)
	if s.UnpushedCommits != nil && *s.UnpushedCommits > 0 && s.State != "failed" {
		if branch, _ := gitx.Branch(k.Root); branch != "" {
			if at, ok, err := gitx.OldestUnpushedAt(k.Root, "origin", branch); err == nil {
				oldest, hasOldest = at, ok
			}
		}
	}
	return PushLagFrom(s, oldest, hasOldest, now, PushLagThreshold)
}

// Remedy classifies a git status error into one fixed sentence a human can
// act on, and never returns any of the error's own text: git and forge output
// carries remote lines, URLs with userinfo and ssh hosts, and this sentence
// goes out on /health. "" when there is no error to classify. No sentence
// names a cartographer command, so it is the same for every reader (D1101).
func Remedy(errText string) string {
	switch {
	case strings.TrimSpace(errText) == "":
		return ""
	case gitx.IsIdentityRejection(errText):
		return "the remote refused the push because of who authored or committed its commits"
	case gitx.IsProtectedBranchRejection(errText):
		return "the remote refused the push: the branch is protected or a server-side hook declined it"
	}
	if r := gitx.RemoteRemedy(errText); r != "" {
		return r
	}
	return "the push failed; the cause is in the server's log"
}
