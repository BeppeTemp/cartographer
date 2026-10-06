package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// --- kb_review ---

// kbReviewMaxLimit bounds one page of the work list.
const kbReviewMaxLimit = 100

// toolKBReview returns the doctor's work list (D298): ranked candidates the
// server builds deterministically and never decides. Read-only; it shares the
// D294 cache with kb_status, and the per-caller visibility filter is applied
// outside it.
func toolKBReview(k *kb.KB, cc *conformanceCache) Tool {
	return Tool{
		Name:     "kb_review",
		ReadOnly: true,
		// The kinds are not listed here: they grow per release (D301) and
		// are in docs/control-plane.md and in each item's kind.
		Description: "Ranked kb-doctor work list: items {kind, concepts, evidence, suggested_action}. " +
			"Dismiss: lint_ignore: [kind] on a named concept.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"kind": {"type": "string"},
				"scope": {"type": "string"},
				"limit": {"type": "integer"},
				"offset": {"type": "integer"}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Kind   string `json:"kind"`
				Scope  string `json:"scope"`
				Limit  int    `json:"limit"`
				Offset int    `json:"offset"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.Kind != "" && !validReviewKind(params.Kind) {
				return errorResult(fmt.Sprintf("kb_review: 'kind' must be one of %s, got %q", strings.Join(lint.ReviewKinds, ", "), params.Kind)), nil
			}
			if params.Limit <= 0 {
				params.Limit = 20
			}
			requestedLimit := params.Limit
			params.Limit = min(params.Limit, kbReviewMaxLimit)
			params.Offset = max(params.Offset, 0)

			items, err := visibleReview(ctx, k, cc)
			if err != nil {
				return errorResult(fmt.Sprintf("kb_review: %v", err)), nil
			}
			scope := strings.TrimSuffix(strings.ReplaceAll(params.Scope, "\\", "/"), "/")
			byKind := map[string]int{}
			var selected []lint.ReviewItem
			for _, it := range items {
				if scope != "" && !reviewInScope(it, scope) {
					continue
				}
				byKind[it.Kind]++
				if params.Kind == "" || it.Kind == params.Kind {
					selected = append(selected, it)
				}
			}
			total := len(selected)
			page := []lint.ReviewItem{}
			if params.Offset < total {
				page = selected[params.Offset:min(params.Offset+params.Limit, total)]
			}
			result := map[string]interface{}{
				"total":   total,
				"by_kind": byKind,
				"offset":  params.Offset,
				"items":   page,
			}
			// A clamped limit must not read as "that was everything" (D318).
			result["limit_applied"] = params.Limit
			if requestedLimit > kbReviewMaxLimit {
				result["limit_capped"] = true
				result["limit_max"] = kbReviewMaxLimit
			}
			if next := params.Offset + len(page); next < total {
				result["next_offset"] = next
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

func validReviewKind(kind string) bool {
	for _, k := range lint.ReviewKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// reviewInScope reports whether an item names a concept under scope.
func reviewInScope(it lint.ReviewItem, scope string) bool {
	for _, id := range it.Concepts {
		if id == scope || strings.HasPrefix(id, scope+"/") {
			return true
		}
	}
	return false
}

// visibleReview is the cached work list filtered for this caller (D226).
func visibleReview(ctx requestContext, k *kb.KB, cc *conformanceCache) ([]lint.ReviewItem, error) {
	items, err := cc.reviewItems(k)
	if err != nil {
		return nil, err
	}
	return lint.FilterReview(items, func(id string) bool { return Visible(ctx, k, id) }, WholeVisible(ctx, k, false)), nil
}

// reviewSummary is kb_status.review: the size of the work list by kind.
func reviewSummary(items []lint.ReviewItem) map[string]interface{} {
	byKind := map[string]int{}
	for _, it := range items {
		byKind[it.Kind]++
	}
	return map[string]interface{}{"total": len(items), "by_kind": byKind}
}

// --- similar on creation (D298) ---

// similarMax caps the advice a creation returns; similarSearchWindow is how
// many search hits are considered. A hit qualifies when its title shares at
// least lint.TitleJaccardMin of its folded words with the new title: the
// search score is corpus-relative (BM25 plus the centrality prior), so it
// ranks the candidates but cannot be the threshold.
const (
	similarMax          = 3
	similarSearchWindow = 10
)

type similarHit struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Score float64 `json:"score"`
}

// similarFinder runs the search tool's ranking for the write tools. Nil
// finds nothing, so a tool built without one (a unit test) stays valid.
type similarFinder struct {
	k    *kb.KB
	rec  *searchReconciler
	deps Deps
}

// find returns the existing concepts whose title nearly matches title,
// excluding id itself, best search score first. Advice only: nil when none.
func (sf *similarFinder) find(ctx requestContext, id, title string) []similarHit {
	if sf == nil || strings.TrimSpace(title) == "" {
		return nil
	}
	want := lint.TitleTokens(title)
	if len(want) == 0 {
		return nil
	}
	hits, _, _ := expandedKeywordHits(ctx, sf.k, sf.rec, sf.deps, title, "", similarSearchWindow, false)
	var out []similarHit
	for _, h := range hits {
		if h.ID == id || lint.Jaccard(want, lint.TitleTokens(h.Title)) < lint.TitleJaccardMin {
			continue
		}
		out = append(out, similarHit{ID: h.ID, Title: h.Title, Score: h.Score})
		if len(out) == similarMax {
			break
		}
	}
	return out
}

// conceptIsNew reports whether id names no concept yet.
func conceptIsNew(k *kb.KB, id string) bool {
	_, err := k.ReadConcept(okf.ConceptID(id))
	return errors.Is(err, okf.ErrNotFound)
}

// frontmatterTitle is the title of a just-written concept, or "".
func frontmatterTitle(k *kb.KB, id string) string {
	data, err := k.ReadConcept(okf.ConceptID(id))
	if err != nil {
		return ""
	}
	fmRaw, _, _ := okf.SplitFrontmatter(data.Content)
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil || fm == nil {
		return ""
	}
	title, _ := fm.Get("title")
	s, _ := title.(string)
	return s
}
