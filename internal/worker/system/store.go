package system

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/worker/feed"
)

// CallFunc calls an MCP tool and returns its text. A tool result that is an
// error (the server refused) is a *Refusal; any other error is the transport's.
// The worker adapts its ToolCaller to it, so this package needs no worker types.
type CallFunc func(tool string, args map[string]any) (string, error)

// Refusal is a tool result with IsError.
type Refusal struct{ Text string }

func (r *Refusal) Error() string { return r.Text }

// Store keeps the state of one system Source in an asset of that Source. The
// asset is its own compare-and-swap object, committed on its own, not atomically
// with the Source's frontmatter: the digest in feed_cursor is what ties them.
type Store struct {
	Call   CallFunc
	Source string // the system Source ID, map/concept
}

// Loaded is the state as found on the Source.
type Loaded struct {
	State   State
	Digest  string // sha256 of the file; "" when there is none
	Trusted bool   // false: the previous state is taken as empty and the poll is a baseline
	Reason  string // why it is not trusted, for the run record
}

// Load reads the state file. No file and no cursor is a first run (trusted,
// empty). Anything else that fails the digest check is untrusted and rebuilt,
// not repaired (decision 7). A read error other than "not found" is returned:
// the poll records it as state_error.
func (s *Store) Load(cursor string) (Loaded, error) {
	text, err := s.Call("asset_read", map[string]any{"concept_id": s.Source, "path": StateFile, "encoding": "text"})
	var ref *Refusal
	switch {
	case errors.As(err, &ref):
		t := strings.ToLower(ref.Text)
		switch {
		case strings.Contains(t, "not found") || strings.Contains(t, "must be expanded first"):
			if cursor == "" {
				return Loaded{State: State{}, Trusted: true}, nil
			}
			return Loaded{State: State{}, Reason: "the state file is missing"}, nil
		case strings.Contains(t, "exceeds"):
			return Loaded{State: State{}, Reason: "the state file is over the asset cap"}, nil
		}
		return Loaded{}, fmt.Errorf("reading the state: %s", clip(ref.Text, 200))
	case err != nil:
		return Loaded{}, fmt.Errorf("reading the state: %w", err)
	}
	var a struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if json.Unmarshal([]byte(text), &a) != nil {
		return Loaded{}, errors.New("reading the state: unreadable asset_read answer")
	}
	l := Loaded{State: State{}, Digest: Digest([]byte(a.Content))}
	switch {
	case a.Encoding != "text":
		l.Reason = "the state file is not text"
	case l.Digest != cursor:
		l.Reason = "the state file does not match feed_cursor"
	default:
		st, derr := Decode([]byte(a.Content))
		if derr != nil {
			l.Reason = derr.Error()
		} else {
			l.State, l.Trusted = st, true
		}
	}
	return l, nil
}

// Save writes next as the state and returns its digest (the new feed_cursor) and
// the working hash of the Source for the next patch. hash is the Source's
// content hash after the lease; leasedAt the feed_checked_at that lease wrote.
// An unchanged state writes nothing. feed.ErrLeaseLost means another replica
// (or a person) got there first: stop, write nothing more; the units exist and
// are idempotent.
func (s *Store) Save(l Loaded, next State, hash, leasedAt, reason string) (digest, newHash string, err error) {
	if l.Digest == "" && len(next) == 0 {
		return "", hash, nil // nothing was ever handled: no file, no cursor
	}
	data, digest, err := next.Encode()
	if err != nil {
		return "", hash, err
	}
	if l.Digest == digest {
		return digest, hash, nil
	}
	// 1. Expand. Already expanded is success; the first state write of a Source
	// from source_register expands it (same ID, links kept).
	if _, err := s.Call("concept_expand", map[string]any{"id": s.Source}); err != nil && !strings.Contains(err.Error(), "already_expanded") {
		return "", hash, stateErr(err)
	}
	// Expand takes no hash: re-read, and go on only if the lease is still ours.
	src, err := s.read()
	if err != nil {
		return "", hash, err
	}
	if got, _ := src.Frontmatter[feed.KeyCheckedAt].(string); got != leasedAt {
		return "", hash, feed.ErrLeaseLost
	}
	hash = src.ContentHash
	// 2. The file, under if_match = the current file's digest (omitted when absent).
	args := map[string]any{"concept_id": s.Source, "path": StateFile, "content": string(data), "encoding": "text"}
	if l.Digest != "" {
		args["if_match"] = l.Digest
	}
	if _, err := s.Call("asset_write", args); err != nil {
		return "", hash, stateErr(err)
	}
	// 3. First time only: the body links the file. The asset exists by now, so
	// the link is no broken_link; a link first would be (write gate, D907) and a
	// file nobody cites is an orphan_asset in lint.
	if !strings.Contains(src.Body, "("+StateFile+")") {
		body := strings.TrimSpace(src.Body)
		out, err := s.Call("concept_patch", map[string]any{"id": s.Source, "if_match": hash, "reason": reason,
			"old_string": body, "new_string": body + "\n\nObserved state: [" + StateFile + "](" + StateFile + ")"})
		if err != nil {
			return "", hash, stateErr(err)
		}
		var o struct {
			Hash string `json:"content_hash"`
		}
		_ = json.Unmarshal([]byte(out), &o)
		hash = o.Hash
	}
	return digest, hash, nil
}

type source struct {
	ContentHash string         `json:"content_hash"`
	Frontmatter map[string]any `json:"frontmatter"`
	Body        string         `json:"body"`
}

func (s *Store) read() (source, error) {
	var src source
	out, err := s.Call("concept_read", map[string]any{"id": s.Source, "full": true})
	if err != nil {
		return src, stateErr(err)
	}
	if json.Unmarshal([]byte(out), &src) != nil || src.ContentHash == "" {
		return src, errors.New("reading the Source: unreadable concept_read answer")
	}
	return src, nil
}

// stateErr maps a stale_write to the lease loss and keeps any other refusal as
// a plain error (the policy refusal of a token narrowed to other maps is one).
func stateErr(err error) error {
	var ref *Refusal
	if errors.As(err, &ref) {
		if strings.Contains(ref.Text, "stale_write") {
			return feed.ErrLeaseLost
		}
		return fmt.Errorf("writing the state: %s", clip(ref.Text, 200))
	}
	return fmt.Errorf("writing the state: %w", err)
}
