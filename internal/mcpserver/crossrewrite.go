package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// crossRewriteReason is the only `reason` the follow-up layer sets on a commit
// in another KB. The reason of the origin call is text of the origin KB: a
// Reason trailer in KB X would show it to everyone who reads X and not the
// origin. No id, no KB name, no text of the origin (a constant, no verb).
const crossRewriteReason = "cross-KB links updated after a move in another KB"

// crossChunk is how many retargets one cross_link_rewrite call takes.
const crossChunk = 50

// withCrossRewrite wraps a gitWrap'ped move tool (concept_move, concept_archive):
// after the origin committed it retargets the cross-KB links to the moved pages
// in every other KB the caller can write, one KB at a time, and adds what
// happened to the result as `cross_kb`.
//
// Lock order, the argument in one place: the wrapper holds nothing when it
// starts, and the inner gitWrap has released the origin's lock when it returns.
// Each follow-up is X's own cross_link_rewrite call (Deps.CallKB), which takes
// X's lock inside X's gitWrap; the next starts after the previous returned. No
// code here ever holds two KB locks, so it cannot deadlock with twoKBMove's
// root-path order or with a move in the opposite direction. A write to X between
// the origin's commit and the follow-up is harmless: the follow-up reads X afresh
// under X's lock. A crash in between leaves the links dangling, found by lint and
// fixed by cross_link_rewrite. The origin is never undone because of another KB.
// Never call this from a handler or AfterCommit: both run under the lock.
func withCrossRewrite(deps Deps, k *kb.KB, t Tool) Tool {
	inner := t.Handler
	t.Handler = func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
		res, err := inner(ctx, args)
		if err != nil || res.IsError || res.CommitSHA == "" || len(res.crossMoves) == 0 || deps.CallKB == nil || isUnattended(ctx) {
			return res, err
		}
		return mergeCrossReport(res, crossFollowUps(ctx, deps, k, res.crossMoves)), nil
	}
	return t
}

// crossMovesOf turns a move's old -> new id map into retargets of this KB's
// current name, sorted. Empty when the KB has no mount name.
func crossMovesOf(k *kb.KB, moveMap map[string]string) []kb.CrossRetarget {
	name := k.MountName()
	if name == "" {
		return nil
	}
	out := make([]kb.CrossRetarget, 0, len(moveMap))
	for from, to := range moveMap {
		if from != to {
			out = append(out, kb.CrossRetarget{FromKB: name, FromID: from, ToKB: name, ToID: to})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FromID < out[j].FromID })
	return out
}

// crossWritable says whether the caller may have a cross link of KB name
// rewritten: the whole-KB write grant (the line concept_move already draws for
// rewriting inbound links), a roster right, the KB served by the connection and
// not read-only on it. Stricter than reading on purpose.
func crossWritable(ctx context.Context, k *kb.KB, name string) bool {
	target, canonical, mounted := crossMounted(k, name)
	if !mounted || !kbRosterVisible(ctx, canonical) || !WholeVisible(ctx, target, true) {
		return false
	}
	if set := routedSetFrom(ctx); set != nil && !containsString(set, canonical) {
		return false
	}
	return !containsString(routedReadOnly(ctx), canonical)
}

// The one report shape, in the origin's JSON under `cross_kb`.
type (
	crossRewritten struct {
		KB    string `json:"kb"`
		Pages int    `json:"pages"`
		Links int    `json:"links"`
	}
	crossFailed struct {
		KB    string `json:"kb"`
		Error string `json:"error"`
	}
	crossNotRewritten struct {
		KB    string `json:"kb"`
		Pages int    `json:"pages"` // distinct pages the caller sees, not links
	}
	crossRetargetArg struct {
		FromKB string `json:"from_kb"`
		FromID string `json:"from_id"`
		ToKB   string `json:"to_kb"`
		ToID   string `json:"to_id"`
	}
	crossReport struct {
		Rewritten    []crossRewritten    `json:"rewritten,omitempty"`
		Failed       []crossFailed       `json:"failed,omitempty"`
		NotRewritten []crossNotRewritten `json:"not_rewritten,omitempty"`
		Skipped      []string            `json:"skipped,omitempty"`
		Note         string              `json:"note,omitempty"`
		Retry        []crossRetargetArg  `json:"retry_retargets,omitempty"`
	}
)

func (r crossReport) empty() bool {
	return len(r.Rewritten) == 0 && len(r.Failed) == 0 && len(r.NotRewritten) == 0 && len(r.Skipped) == 0
}

const crossFailedNote = "the move is committed; links in the listed KBs still point at the old id: run cross_link_rewrite there with the same retargets (idempotent), see retry_retargets"

// crossFollowUps retargets the links to moves in the other KBs, in sorted name
// order. It says nothing about a KB the caller cannot read.
func crossFollowUps(ctx context.Context, deps Deps, k *kb.KB, moves []kb.CrossRetarget) crossReport {
	var rep crossReport
	incoming, err := k.CrossIncomingAll()
	if err != nil {
		log.Printf("cartographer: cross-KB follow-up of %q skipped: %v", k.MountName(), err)
		return rep
	}
	byID := make(map[string]kb.CrossRetarget, len(moves))
	for _, mv := range moves {
		byID[mv.FromID] = mv
	}
	pagesOf := map[string][]string{}        // KB -> its pages that link a moved page
	movesOf := map[string]map[string]bool{} // KB -> old ids it links
	for _, in := range incoming {
		if _, moved := byID[string(in.Target)]; !moved {
			continue
		}
		pagesOf[in.KB] = append(pagesOf[in.KB], string(in.ID))
		if movesOf[in.KB] == nil {
			movesOf[in.KB] = map[string]bool{}
		}
		movesOf[in.KB][string(in.Target)] = true
	}
	names := make([]string, 0, len(pagesOf))
	for n := range pagesOf {
		if n != k.MountName() {
			names = append(names, n)
		}
	}
	sort.Strings(names)

	// The follow-up is X's own call: the routed set of this connection does not
	// decide for it, X's server does.
	callCtx := withRoutedSet(ctx, nil)
	cancelled := false
	failedRetargets := false
	for _, x := range names {
		if crossWritable(ctx, k, x) {
			if cancelled || ctx.Err() != nil {
				cancelled = true
				rep.Skipped = append(rep.Skipped, x)
				continue
			}
			var rels []crossRetargetArg
			for _, mv := range moves {
				if movesOf[x][mv.FromID] {
					rels = append(rels, crossRetargetArg{mv.FromKB, mv.FromID, mv.ToKB, mv.ToID})
				}
			}
			pages, links, ferr := 0, 0, ""
			for start := 0; start < len(rels) && ferr == ""; start += crossChunk {
				end := min(start+crossChunk, len(rels))
				args, _ := json.Marshal(map[string]any{"retargets": rels[start:end], "reason": crossRewriteReason})
				res := deps.CallKB(callCtx, x, "cross_link_rewrite", args)
				p, l, msg := crossCallOutcome(res)
				pages, links = pages+p, links+l
				ferr = msg
			}
			switch {
			case ferr != "":
				rep.Failed = append(rep.Failed, crossFailed{KB: x, Error: ferr})
				if !failedRetargets {
					failedRetargets = true
					for _, mv := range moves {
						rep.Retry = append(rep.Retry, crossRetargetArg{mv.FromKB, mv.FromID, mv.ToKB, mv.ToID})
					}
				}
			case pages > 0:
				rep.Rewritten = append(rep.Rewritten, crossRewritten{KB: x, Pages: pages, Links: links})
			}
			continue
		}
		// Readable, not writable: how many links it still holds, from the pages
		// the caller can see. A KB the connection does not serve, or the caller
		// cannot read, is not named at all.
		if set := routedSetFrom(ctx); (set != nil && !containsString(set, x)) || !crossReadable(ctx, k, x) {
			continue
		}
		target, _, _ := crossMounted(k, x)
		seen := map[string]bool{}
		for _, id := range pagesOf[x] {
			if !seen[id] && Visible(ctx, target, id) {
				seen[id] = true
			}
		}
		if len(seen) > 0 {
			rep.NotRewritten = append(rep.NotRewritten, crossNotRewritten{KB: x, Pages: len(seen)})
		}
	}
	if len(rep.Failed) > 0 {
		rep.Note = crossFailedNote
	}
	return rep
}

// crossCallOutcome reads one cross_link_rewrite result: the counts, or the error
// text X's own pipeline produced.
func crossCallOutcome(res ToolResult) (pages, links int, errText string) {
	text := ""
	if len(res.Content) > 0 {
		text = res.Content[0].Text
	}
	if res.IsError {
		if text == "" {
			text = "cross_link_rewrite failed"
		}
		return 0, 0, text
	}
	var out struct {
		Rewritten struct{ Pages, Links int } `json:"rewritten"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return 0, 0, fmt.Sprintf("unreadable cross_link_rewrite result: %v", err)
	}
	return out.Rewritten.Pages, out.Rewritten.Links, ""
}

// mergeCrossReport adds `cross_kb` to the origin's JSON by decoding and
// re-encoding Content[0] (CLI callers decode that block only), never as a second
// block. On any trouble the result is returned unchanged: the report must never
// break the move's own answer.
func mergeCrossReport(res ToolResult, rep crossReport) ToolResult {
	if rep.empty() || len(res.Content) == 0 {
		return res
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(res.Content[0].Text), &obj); err != nil || obj == nil {
		return res
	}
	raw, err := json.Marshal(rep)
	if err != nil {
		return res
	}
	obj["cross_kb"] = raw
	merged := jsonResult(obj)
	if merged.IsError {
		return res
	}
	content := append([]ContentBlock(nil), res.Content...)
	content[0] = merged.Content[0]
	res.Content = content
	return res
}

// crossDeleteBlockers lists, sorted as "<kb>:<id>", the pages of other KBs that
// link id of this KB (or, for an expanded concept only, a page under it) and that
// the caller may rewrite: a KB it writes whole (crossWritable) and a page it
// sees. A link from any other KB is never listed and never blocks (a refusal
// would show that the link exists); the delete then leaves broken_cross_kb_link
// for that KB's governors. Self-named links are no edge, so none shows. A KB
// whose graph cannot be read is unknown: logged, and no blocker.
func crossDeleteBlockers(ctx context.Context, k *kb.KB, id string) []string {
	in, err := k.CrossIncomingAll()
	if err != nil {
		log.Printf("cartographer: cross-KB links to %q unknown: %v", id, err)
		return nil
	}
	// Only an expanded concept owns what lies under id+"/": a plain page "foo"
	// is not deleted with a page "foo/bar".
	expanded := false
	if loc, err := k.LocateConcept(okf.ConceptID(id)); err == nil {
		expanded = loc.Expanded
	}
	seen := map[string]bool{}
	var out []string
	for _, ref := range in {
		t := string(ref.Target)
		if t != id && !(expanded && strings.HasPrefix(t, id+"/")) {
			continue
		}
		target, _, ok := crossMounted(k, ref.KB)
		if !ok || !crossWritable(ctx, k, ref.KB) || !Visible(ctx, target, string(ref.ID)) {
			continue
		}
		entry := ref.KB + ":" + string(ref.ID)
		if !seen[entry] {
			seen[entry] = true
			out = append(out, entry)
		}
	}
	sort.Strings(out)
	return out
}

// crossKBsOf names, sorted, the KBs of "<kb>:<id>" entries.
func crossKBsOf(entries []string) []string {
	var names []string
	for _, e := range entries {
		if kbName, _, ok := strings.Cut(e, ":"); ok {
			names = append(names, kbName)
		}
	}
	return uniqueSorted(names)
}

func uniqueSorted(in []string) []string {
	sort.Strings(in)
	return slices.Compact(in)
}
