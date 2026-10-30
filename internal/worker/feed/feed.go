// Package feed reads and patches a feed Source cursor.
//
// A feed is a Source with source_shape: feed. The worker is stateless: the
// cursor of what it has listed so far, the time of the last check and the last
// error live in the Source's own frontmatter, and every write of them is a
// concept_patch with if_match. A stale if_match is how two replicas tell who
// polls a feed, so it is a distinct, silent outcome (ErrLeaseLost), not a
// failure.
//
// The package takes the entries of concept_list as plain data and imports
// neither internal/kb nor internal/mcpserver (the worker area's rule).
package feed

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Frontmatter keys of a feed Source.
const (
	KeyCursor    = "feed_cursor"
	KeyCheckedAt = "feed_checked_at"
	KeyInterval  = "feed_interval"
	KeyInclude   = "feed_include"
	KeyError     = "feed_error"
)

// QueryFields are the frontmatter keys the poll asks concept_list for: exactly
// the 8-key cap of its fields parameter, so no per-feed read is needed. The
// content hash (if_match of the first write) is requested with the content_hash
// pseudo-field, appended by the caller (QueryFieldsWithHash).
var QueryFields = []string{"locator", "connector", "source_kind", "recipe", KeyCheckedAt, KeyInterval, KeyCursor, KeyInclude}

// QueryFieldsWithHash is QueryFields plus the content_hash pseudo-field.
func QueryFieldsWithHash() []string {
	return append(append([]string{}, QueryFields...), "content_hash")
}

// Limits on what a Source may hold in the feed fields.
const (
	MaxCursorBytes = 1024
	MaxErrorBytes  = 300
)

// Codes of feed_error written by this package; the poll task adds its own.
const (
	CodeBadField = "bad_field"
)

// ErrLeaseLost is returned (by the caller, from a stale_write answer) when the
// Source changed since the hash the patch was built from: another replica or a
// person got there first. It is not an error to report.
var ErrLeaseLost = errors.New("feed: the Source changed since it was read (lease lost)")

// Entry is one row of concept_list: the concept ID, the requested frontmatter
// keys (a string for a scalar, a []string for a list) and the content hash.
type Entry struct {
	ID          string
	Fields      map[string]any
	ContentHash string
}

// Error is a feed_error to record: Code is one of the poll's codes, Detail one line.
type Error struct{ Code, Detail string }

func (e *Error) Error() string { return e.Code + ": " + e.Detail }

// Feed is a parsed feed Source.
type Feed struct {
	ID          string
	Locator     string
	Connector   string
	SourceKind  string
	Recipe      string
	Cursor      string
	Include     []string
	Interval    time.Duration // the Source's own, or the default
	CheckedAt   time.Time     // zero: never checked
	ContentHash string        // if_match of the next write
}

// Parse validates the feed fields of e. interval and minInterval come from the
// poll task: default_interval applies when the Source sets none, and
// min_interval is the floor of any feed_interval. A violation is an *Error with
// code bad_field, never a panic.
func Parse(e Entry, defaultInterval, minInterval time.Duration) (Feed, error) {
	f := Feed{ID: e.ID, ContentHash: e.ContentHash, Interval: defaultInterval}
	bad := func(format string, args ...any) (Feed, error) {
		return Feed{}, &Error{Code: CodeBadField, Detail: fmt.Sprintf(format, args...)}
	}
	f.Locator, _ = e.Fields["locator"].(string)
	f.Connector, _ = e.Fields["connector"].(string)
	f.SourceKind, _ = e.Fields["source_kind"].(string)
	f.Recipe, _ = e.Fields["recipe"].(string)

	if v, ok := e.Fields[KeyInterval]; ok {
		s, isStr := v.(string)
		d, err := time.ParseDuration(strings.TrimSpace(s))
		switch {
		case !isStr || err != nil:
			return bad("%s is not a duration such as 6h", KeyInterval)
		case d <= 0:
			return bad("%s must be above zero", KeyInterval)
		case d < minInterval:
			return bad("%s %s is below the task's min_interval %s", KeyInterval, d, minInterval)
		}
		f.Interval = d
	}
	if v, ok := e.Fields[KeyCheckedAt]; ok {
		s, isStr := v.(string)
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
		if !isStr || err != nil {
			return bad("%s is not an RFC3339 time", KeyCheckedAt)
		}
		f.CheckedAt = t
	}
	if v, ok := e.Fields[KeyInclude]; ok {
		switch l := v.(type) {
		case []string:
			f.Include = l
		case string:
			f.Include = []string{l}
		default:
			return bad("%s is not a list of globs", KeyInclude)
		}
		for _, g := range f.Include {
			if strings.TrimSpace(g) == "" {
				return bad("%s holds an empty glob", KeyInclude)
			}
		}
	}
	if v, ok := e.Fields[KeyCursor]; ok {
		s, isStr := v.(string)
		if !isStr {
			return bad("%s is not a string", KeyCursor)
		}
		if len(s) > MaxCursorBytes {
			return bad("%s is over %d bytes", KeyCursor, MaxCursorBytes)
		}
		f.Cursor = s
	}
	return f, nil
}

// Due reports whether the feed is to be polled at now: never checked, or
// CheckedAt plus Interval has passed.
func (f Feed) Due(now time.Time) bool {
	return f.CheckedAt.IsZero() || !now.Before(f.CheckedAt.Add(f.Interval))
}

// The three builders return the arguments of a concept_patch. The cursor and the
// bookkeeping are written ONLY with if_match, always: it is the compare-and-swap
// that keeps two replicas from polling one feed and keeps a person's edit from
// being overwritten. ifMatch is the content hash the Source was last read at
// (the first write's comes from the query, each next from the previous answer).

// Lease claims the feed for this poll by setting feed_checked_at to now.
func (f Feed) Lease(now time.Time) (map[string]any, error) {
	if f.ContentHash == "" {
		return nil, errNoIfMatch
	}
	return patch(f.ID, f.ContentHash, now, nil), nil
}

// Advance records that every entry up to cursor is handled: it sets feed_cursor
// and feed_checked_at and removes feed_error. ifMatch is the hash Lease returned.
func (f Feed) Advance(ifMatch, cursor string, now time.Time) (map[string]any, error) {
	if ifMatch == "" {
		return nil, errNoIfMatch
	}
	if len(cursor) > MaxCursorBytes {
		return nil, fmt.Errorf("feed: cursor is %d bytes, over the %d the Source holds", len(cursor), MaxCursorBytes)
	}
	a := patch(f.ID, ifMatch, now, map[string]any{KeyCursor: cursor})
	a["unset"] = []string{KeyError}
	return a, nil
}

var errNoIfMatch = errors.New("feed: a write of the cursor or its bookkeeping needs an if_match")

// Fail records why the feed could not be polled, and moves feed_checked_at so a
// failing feed is retried at its interval and never in a tight loop. The cursor
// is left alone. The detail is cut to one line of MaxErrorBytes.
func (f Feed) Fail(ifMatch, code, detail string, now time.Time) (map[string]any, error) {
	if ifMatch == "" {
		return nil, errNoIfMatch
	}
	return patch(f.ID, ifMatch, now, map[string]any{KeyError: errorLine(code, detail)}), nil
}

func patch(id, ifMatch string, now time.Time, set map[string]any) map[string]any {
	fm := map[string]any{KeyCheckedAt: now.UTC().Format(time.RFC3339)}
	for k, v := range set {
		fm[k] = v
	}
	return map[string]any{"id": id, "if_match": ifMatch, "frontmatter": fm}
}

func errorLine(code, detail string) string {
	detail = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, detail)
	s := code + ": " + strings.TrimSpace(detail)
	if len(s) > MaxErrorBytes {
		s = s[:MaxErrorBytes]
		for len(s) > 0 && !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

// Outcome maps the error text of a failed concept_patch on a feed Source to
// ErrLeaseLost when it is a stale_write, and to a plain error otherwise; empty
// text is success (nil).
func Outcome(errText string) error {
	switch {
	case errText == "":
		return nil
	case strings.HasPrefix(errText, "stale_write"):
		return ErrLeaseLost
	}
	return errors.New(errText)
}
