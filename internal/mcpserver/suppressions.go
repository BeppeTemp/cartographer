package mcpserver

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

// The suppression inventory of kb_status (D834): what each lint_ignore /
// lint_accept entry hides, since when and why. The counts come from the cached
// lint run; the date and reason are derived from git on request, never stored
// next to the entry.
const (
	// suppressionListMax caps suppressions.list.
	suppressionListMax = 200
	// suppressionHistoryScan is how many commits of one file the walk reads: an
	// entry older than that has no `added`.
	suppressionHistoryScan = 200
	// suppressionRevisionBudget caps the file revisions (one `git show` each)
	// one kb_status call reads: 200 entries on 200 files of 200 commits would
	// otherwise spawn ~40,000 git processes. Past it the remaining entries
	// carry no origin and the section says origins_truncated.
	suppressionRevisionBudget = 1000
)

// suppressionItem is one entry of suppressions.list.
type suppressionItem struct {
	Source string `json:"source"`
	Path   string `json:"path"`
	Check  string `json:"check"`
	Hidden int    `json:"hidden"`
	Stale  bool   `json:"stale,omitempty"`
	// Added, Commit and Reason come from the commit that started the entry's
	// current unbroken presence; absent when git cannot say.
	Added  string `json:"added,omitempty"`
	Commit string `json:"commit,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// visibleSuppressions keeps the entries whose file the caller can see. An
// instructions.md entry (lint_accept) and a map descriptor are KB-level: whole-KB
// callers only, like the findings on them (findingConcept).
func visibleSuppressions(ctx requestContext, k *kb.KB, all []lint.Suppression) []lint.Suppression {
	whole := WholeVisible(ctx, k, false)
	var out []lint.Suppression
	for _, sp := range all {
		if sp.Source == lint.SourceAccept {
			if whole {
				out = append(out, sp)
			}
			continue
		}
		if concept := uiFindingConcept(sp.Path); concept != "" {
			if Visible(ctx, k, concept) {
				out = append(out, sp)
			}
		} else if whole {
			out = append(out, sp)
		}
	}
	return out
}

// suppressionsSummary is the always-present part: totals over what the caller
// sees; nil when there are no entries.
func suppressionsSummary(sups []lint.Suppression) map[string]interface{} {
	if len(sups) == 0 {
		return nil
	}
	hidden, stale := 0, 0
	for _, sp := range sups {
		hidden += sp.Hidden
		if sp.Stale {
			stale++
		}
	}
	return map[string]interface{}{"entries": len(sups), "hidden": hidden, "stale": stale}
}

// suppressionsList is the on-request part: entries by hidden count, then path,
// at most suppressionListMax, each with what git says about its origin.
func suppressionsList(k *kb.KB, sups []lint.Suppression, revisionBudget int) (items []suppressionItem, truncated, originsTruncated bool) {
	sorted := append([]lint.Suppression(nil), sups...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Hidden != b.Hidden {
			return a.Hidden > b.Hidden
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Check < b.Check
	})
	if len(sorted) > suppressionListMax {
		sorted, truncated = sorted[:suppressionListMax], true
	}
	origins := suppressionOrigins{k: k, byFile: map[string]*fileOrigins{}, budget: revisionBudget}
	items = make([]suppressionItem, 0, len(sorted))
	for _, sp := range sorted {
		it := suppressionItem{Source: sp.Source, Path: sp.Path, Check: sp.Check, Hidden: sp.Hidden, Stale: sp.Stale}
		if rev, ok := origins.of(sp); ok {
			it.Added, it.Commit = rev.At.UTC().Format("2006-01-02"), rev.SHA[:min(12, len(rev.SHA))]
			if it.Reason = rev.Reason; it.Reason == "" {
				it.Reason = rev.Subject
			}
		}
		items = append(items, it)
	}
	return items, truncated, origins.exhausted
}

// suppressionOrigins walks the history of each file once and shares it among
// its entries.
type suppressionOrigins struct {
	k      *kb.KB
	byFile map[string]*fileOrigins
	// budget is the file revisions still readable in this call; exhausted
	// says a file was skipped for lack of it.
	budget    int
	exhausted bool
}

// fileOrigins is one file's walk: per declared name (key + "\x00" + check for
// lint_accept), the revision that started its current unbroken presence.
type fileOrigins struct {
	start map[string]gitx.FileRevision
}

func (o *suppressionOrigins) of(sp lint.Suppression) (gitx.FileRevision, bool) {
	if !o.k.AutoCommit || !gitx.IsRepo(o.k.Root) {
		return gitx.FileRevision{}, false
	}
	path := suppressionGitPath(o.k, sp)
	fo, ok := o.byFile[path]
	if !ok {
		fo = walkSuppressionFile(o, sp.Source, path)
		o.byFile[path] = fo
	}
	name := sp.Check
	if sp.Source == lint.SourceAccept {
		name = sp.Path + "\x00" + sp.Check
	}
	rev, ok := fo.start[name]
	return rev, ok
}

// suppressionGitPath is the entry's file relative to the repository root:
// concepts and maps live under data/ (services/ concepts beside it), the
// lint_accept entries on instructions.md.
func suppressionGitPath(k *kb.KB, sp lint.Suppression) string {
	if sp.Source == lint.SourceAccept {
		return "instructions.md"
	}
	if _, err := os.Stat(filepath.Join(k.DataRoot(), filepath.FromSlash(sp.Path))); err != nil {
		if _, err2 := os.Stat(filepath.Join(k.Root, filepath.FromSlash(sp.Path))); err2 == nil {
			return sp.Path
		}
	}
	rel, err := filepath.Rel(k.Root, k.DataRoot())
	if err != nil {
		return sp.Path
	}
	return filepath.ToSlash(filepath.Join(rel, filepath.FromSlash(sp.Path)))
}

// walkSuppressionFile reads the file at each revision, oldest to newest, and
// keeps for every name the revision where its current unbroken presence began.
// A name present in the oldest revision read, of a history cut at the scan
// limit, has an unknown origin and is left out; so is everything when a
// revision cannot be read.
func walkSuppressionFile(o *suppressionOrigins, source, gitPath string) *fileOrigins {
	k := o.k
	fo := &fileOrigins{start: map[string]gitx.FileRevision{}}
	revs, err := gitx.FileHistory(k.Root, gitPath, suppressionHistoryScan)
	if err != nil || len(revs) == 0 {
		return fo
	}
	if len(revs) > o.budget {
		o.exhausted = true // all or nothing: a half-read file would date wrongly
		return fo
	}
	o.budget -= len(revs)
	cut := len(revs) >= suppressionHistoryScan
	// accept entries share one file: names are (key, check) pairs.
	names := func(content string) map[string]bool {
		if source != lint.SourceAccept {
			return lint.DeclaredEntries(source, content, "")
		}
		out := map[string]bool{}
		for _, key := range lint.AcceptKeys(content) {
			for c := range lint.DeclaredEntries(source, content, key) {
				out[key+"\x00"+c] = true
			}
		}
		return out
	}
	present := map[string]bool{}
	for i := len(revs) - 1; i >= 0; i-- {
		rev := revs[i]
		var cur map[string]bool
		if rev.Status != "D" {
			content, err := gitx.ShowFile(k.Root, rev.SHA, rev.Path)
			if err != nil {
				return &fileOrigins{start: map[string]gitx.FileRevision{}}
			}
			cur = names(content)
		}
		for name := range cur {
			if !present[name] {
				if i == len(revs)-1 && cut {
					continue // already there before the window: origin unknown
				}
				fo.start[name] = rev
			}
		}
		for name := range present {
			if !cur[name] {
				delete(fo.start, name)
			}
		}
		present = cur
	}
	return fo
}
