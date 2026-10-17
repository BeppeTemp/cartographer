package mcpserver

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// The retired route (D820): what the KB has retired, where it went,
// when and why. Retirement is only a status in the page itself; the from-path,
// the date and the reason live in the root log (kb.Retirements), so a page
// archived long ago still has its story. Read-only like every UI route.

// archiveCount is a map's top-level concepts and how many of them are
// retired: the two numbers kb.IsArchiveMap decides on.
type archiveCount struct{ top, retired int }

// archiveCounts counts, per top-level collection, the concepts directly in it
// (an id of exactly two segments, as the index_lists_retired exemption counts
// them) and the retired ones.
func archiveCounts(entries []ConceptEntry) map[string]archiveCount {
	out := map[string]archiveCount{}
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.ID, e.Collection+"/")
		if e.Collection == "" || !ok || strings.Contains(rest, "/") {
			continue
		}
		c := out[e.Collection]
		c.top++
		if kb.IsRetired(e.Status) {
			c.retired++
		}
		out[e.Collection] = c
	}
	return out
}

// isArchiveCollection applies kb.IsArchiveMap to one collection: the rule the
// lint exemption uses, so the overview's archive flag cannot disagree with it.
func isArchiveCollection(k *kb.KB, name string, c archiveCount) bool {
	contract, _ := k.ReadMapContract(name) // unreadable: the zero contract, not a journal
	return kb.IsArchiveMap(contract, c.top, c.retired)
}

type retiredConcept struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	Status    string `json:"status"`
	Map       string `json:"map,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	// SupersededBy and its title are shown only when the caller sees the
	// successor: a hidden id is not named here.
	SupersededBy      string `json:"superseded_by,omitempty"`
	SupersededByTitle string `json:"superseded_by_title,omitempty"`
	SupersedeReason   string `json:"supersede_reason,omitempty"`
	// RetiredAt, From and Reason come from the log entries that retired the
	// concept; all empty when none matches (a status set by hand). From is
	// the id it had before concept_archive moved it; it and Reason (free text
	// that may name it) are omitted when the caller cannot see that id.
	RetiredAt string `json:"retired_at,omitempty"`
	From      string `json:"from,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type retiredMap struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Kind    string `json:"kind"`
	Archive bool   `json:"archive"`
	// Retired and Total count the map's top-level concepts, the numbers
	// Archive was decided on.
	Retired int `json:"retired"`
	Total   int `json:"total"`
}

type retiredJournal struct {
	Name         string `json:"name"`
	Title        string `json:"title,omitempty"`
	HarvestAfter int    `json:"harvest_after,omitempty"`
	HarvestOff   bool   `json:"harvest_off,omitempty"`
	// Waiting is the closed entries kb_review offers for harvesting; Archived
	// the ones already harvested.
	Waiting  int `json:"waiting"`
	Archived int `json:"archived"`
}

// GET /kbs/{kb}/retired — every visible retired concept with its story, the
// visible maps with their archive flag, and the visible journals with their
// harvest state. Everything is derived from what the principal sees, so a
// narrowed token learns neither a hidden concept nor a hidden map's counts.
func uiRetired(w http.ResponseWriter, r *http.Request, srv *Server) {
	k := srv.kbRef
	ctx := r.Context()
	res, err := queryConcepts(k, ConceptQuery{
		Fields:  []string{"superseded_by", "supersede_reason"},
		Include: func(id string) bool { return Visible(ctx, k, id) },
	})
	if err != nil {
		writeUIInternal(w, "retired: walk", err)
		return
	}
	retirements, err := k.Retirements()
	if err != nil {
		writeUIInternal(w, "retired: log", err)
		return
	}
	// An id can carry both kinds of entry (archived to archive/a, later
	// superseded in place), and neither replaces the other: the archive says
	// where it came from and why, the supersede that it was replaced. The log is
	// newest first, so the first entry of a kind is the one that counts.
	type story struct{ archive, supersede *kb.Retirement }
	stories := map[string]*story{}
	for i := range retirements {
		rt := &retirements[i]
		st := stories[rt.Subject()]
		if st == nil {
			st = &story{}
			stories[rt.Subject()] = st
		}
		if rt.Status == "superseded" {
			if st.supersede == nil {
				st.supersede = rt
			}
		} else if st.archive == nil {
			st.archive = rt
		}
	}

	concepts := []retiredConcept{}
	archivedIn := map[string]int{}
	for _, e := range res.Entries {
		if e.Status == kb.StatusArchived {
			archivedIn[e.Collection]++
		}
		if !kb.IsRetired(e.Status) {
			continue
		}
		c := retiredConcept{ID: e.ID, Title: e.Title, Status: e.Status, Map: e.Collection, Timestamp: e.Timestamp}
		if by, _ := e.Fields["superseded_by"].(string); by != "" && Visible(ctx, k, by) {
			c.SupersededBy = by
			c.SupersededByTitle = k.ConceptFacets(okf.ConceptID(by)).Title
		}
		c.SupersedeReason, _ = e.Fields["supersede_reason"].(string)
		if st := stories[e.ID]; st != nil {
			// The date is the entry that set the concept's current status; the
			// other kind is the fallback (superseded, then deprecated by hand).
			dated := st.archive
			if (e.Status == "superseded" && st.supersede != nil) || st.archive == nil {
				dated = st.supersede
			}
			if dated != nil && !dated.At.IsZero() {
				c.RetiredAt = dated.At.UTC().Format(time.RFC3339)
			}
			// The reason is free text written next to the from-path: a caller who
			// cannot see where the page came from does not read why it left.
			if rt := st.archive; rt != nil && Visible(ctx, k, rt.From) {
				c.From = rt.From
				c.Reason = rt.Reason
			}
		}
		concepts = append(concepts, c)
	}
	// Newest retirement first; the ones with no recorded date after them, by id.
	sort.SliceStable(concepts, func(i, j int) bool {
		a, b := concepts[i].RetiredAt, concepts[j].RetiredAt
		if (a == "") != (b == "") {
			return a != ""
		}
		if a != b {
			return a > b
		}
		return concepts[i].ID < concepts[j].ID
	})

	waiting := map[string]int{}
	if srv.conformance != nil {
		items, rerr := visibleReview(ctx, k, srv.conformance)
		if rerr != nil {
			writeUIInternal(w, "retired: review", rerr)
			return
		}
		for _, it := range items {
			if it.Kind != lint.ReviewHarvestCandidate || len(it.Concepts) == 0 {
				continue
			}
			if m, _, _ := strings.Cut(it.Concepts[0], "/"); m != "" {
				waiting[m]++
			}
		}
	}

	names, err := k.ListArchives()
	if err != nil {
		writeUIInternal(w, "retired: list maps", err)
		return
	}
	counts := archiveCounts(res.Entries)
	maps := []retiredMap{}
	journals := []retiredJournal{}
	for _, name := range names {
		kind := uiArchiveKind(k, name)
		if !VisibleCollection(ctx, k, name, kind) {
			continue
		}
		title := ""
		if meta, merr := k.ReadArchiveMeta(name); merr == nil {
			if v, ok := meta.Get("title"); ok {
				title, _ = v.(string)
			}
		}
		c := counts[name]
		maps = append(maps, retiredMap{
			Name: name, Title: title, Kind: kind,
			Archive: isArchiveCollection(k, name, c), Retired: c.retired, Total: c.top,
		})
		if kind != "journal" {
			continue
		}
		j := retiredJournal{Name: name, Title: title, Waiting: waiting[name], Archived: archivedIn[name]}
		if contract, cerr := k.ReadMapContract(name); cerr == nil {
			j.HarvestAfter, _, j.HarvestOff = effectiveHarvestAfter(&contract)
		}
		journals = append(journals, j)
	}

	writeUIJSON(w, http.StatusOK, map[string]interface{}{
		"concepts": concepts,
		"maps":     maps,
		"journals": journals,
	})
}
