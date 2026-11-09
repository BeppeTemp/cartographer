package mcpserver

import (
	"log"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

// maxMentions caps the mentions a move reports; the rest is counted in
// `mentions_omitted` (D1070).
const maxMentions = 50

// mentionOut is one plain-text occurrence of a moved concept's old ID.
// Line is 1-based in the file, frontmatter included.
type mentionOut struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Line int    `json:"line"`
	Kind string `json:"kind"` // artifact | frontmatter | prose
}

// mentionConceptID is the concept ID of a concept file path.
func mentionConceptID(p string) string { return lint.MentionConceptID(p) }

// attachMentions sets result["mentions"] and result["mentions_omitted"].
// It only reads: it runs on the final state, after the links, the indexes and
// the log line are written, and nothing is written after it.
//
// candidates, when set, narrows the concept files read to the ones that can
// hold an old ID (nil means "unknown": every concept is read).
func attachMentions(k *kb.KB, result map[string]interface{}, moveMap map[string]string, view *callerView, candidates func(oldIDs []string) map[string]bool) {
	found, err := scanMentions(k, moveMap, candidates)
	if err != nil {
		// What was found is reported; the note says the list is not the whole.
		// The cause goes to the server log only: it carries server paths.
		log.Printf("concept_move: the mentions scan did not finish: %v", err)
		result["mentions_incomplete"] = "the scan did not finish: a mention may be missing, check the old IDs with a search"
	}
	all := view.filterMentions(found)
	if len(all) == 0 {
		return
	}
	if len(all) > maxMentions {
		result["mentions_omitted"] = len(all) - maxMentions
		all = all[:maxMentions]
	}
	result["mentions"] = all
}

// scanMentions lists the plain-text occurrences of the old IDs of a move
// (lint.ScanMentions, shared with the kb_review moved_id_cited observation).
func scanMentions(k *kb.KB, moveMap map[string]string, candidates func(oldIDs []string) map[string]bool) ([]mentionOut, error) {
	found, err := scanMentionsFn(k, moveMap, candidates)
	out := make([]mentionOut, len(found))
	for i, m := range found {
		out[i] = mentionOut{ID: m.ID, Path: m.Path, Line: m.Line, Kind: m.Kind}
	}
	return out, err
}

// scanMentionsFn is lint.ScanMentions, a variable so a test can fail the scan.
var scanMentionsFn = lint.ScanMentions

// mentionCandidates answers the mentions scan of concept_move without reading
// the KB: after bringing the SQLite index in line with the files it asks it
// which stored contents hold an old ID as a substring (a superset of the
// whole-token matches the scan then confirms in those files). nil, meaning
// "scan everything", when there is no SQLite index, either step fails, or any
// earlier SQLite write failed (sqlStale: the failure is only logged and its
// delta consumed, so the index may lack a row for good): the
// index is a derived cache and never decides what the report omits.
func (r *searchReconciler) mentionCandidates(oldIDs []string) map[string]bool {
	if r == nil || r.sql == nil {
		return nil
	}
	stats, err := r.reconcile()
	if err != nil || stats.Failed > 0 {
		return nil
	}
	r.mu.Lock()
	stale := r.sqlStale
	r.mu.Unlock()
	if stale {
		return nil
	}
	set, err := r.sql.IDsContaining(oldIDs)
	if err != nil {
		return nil
	}
	return set
}
