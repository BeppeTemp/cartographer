package mcpserver

import (
	"net/http"
	"sort"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

const (
	maxFeedRows   = 50
	maxFeedErrLen = 300
)

type feedRow struct {
	ID            string `json:"id"`
	Title         string `json:"title,omitempty"`
	SourceShape   string `json:"source_shape,omitempty"`
	Recipe        string `json:"recipe,omitempty"`
	FeedCheckedAt string `json:"feed_checked_at,omitempty"`
	FeedError     string `json:"feed_error"`
}

// GET /kbs/{kb}/feeds — the Source concepts whose last poll failed, for the
// Atlas's Work page. It keys on the non-empty `feed_error` frontmatter field,
// not on a source_shape value: feeds and systems write the same feed_* fields,
// so the route stays right whatever the shape enum holds. Each concept is
// filtered with the caller's principal (no whole-KB gate: a narrowed caller
// sees the feeds of the maps it sees). Text from frontmatter is cut and
// stripped of control characters; the Atlas renders it as text.
func uiFeeds(w http.ResponseWriter, r *http.Request, srv *Server) {
	k := srv.kbRef
	ctx := r.Context()
	rows := []feedRow{}
	err := k.WalkConcepts(func(id okf.ConceptID, content string) error {
		fmRaw, _, _ := okf.SplitFrontmatter(content)
		fm, perr := okf.ParseFrontmatter(fmRaw)
		if perr != nil || fm == nil || !kb.IsSourceType(fm.Type()) {
			return nil
		}
		msg := cleanText(fmString(fm, "feed_error"), maxFeedErrLen)
		if msg == "" || !Visible(ctx, k, string(id)) {
			return nil
		}
		row := feedRow{
			ID: string(id), Title: cleanText(fmString(fm, "title"), 200), FeedError: msg,
			SourceShape: cleanText(fmString(fm, "source_shape"), 40),
			Recipe:      cleanText(fmString(fm, "recipe"), 80),
		}
		if at := fmString(fm, "feed_checked_at"); at != "" {
			if _, err := time.Parse(time.RFC3339, at); err == nil {
				row.FeedCheckedAt = at
			}
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		writeUIInternal(w, "feeds", err)
		return
	}
	// Newest check first (RFC 3339 sorts by text only inside one offset, so
	// compare parsed instants), then id.
	at := func(f feedRow) time.Time { t, _ := time.Parse(time.RFC3339, f.FeedCheckedAt); return t }
	sort.SliceStable(rows, func(i, j int) bool {
		if ai, aj := at(rows[i]), at(rows[j]); !ai.Equal(aj) {
			return ai.After(aj)
		}
		return rows[i].ID < rows[j].ID
	})
	total := len(rows)
	if total > maxFeedRows {
		rows = rows[:maxFeedRows]
	}
	writeUIJSON(w, http.StatusOK, map[string]any{"feeds": rows, "total": total})
}
