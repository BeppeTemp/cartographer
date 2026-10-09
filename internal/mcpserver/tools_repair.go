package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// repairPlannedCap bounds the planned list of a kb_repair response; the total
// is always reported next to it.
const repairPlannedCap = 50

// repairItem is one finding's fix, bound to the concept it applies to.
type repairItem struct {
	Path string    `json:"path"`
	Fix  *lint.Fix `json:"fix"`
}

// repairSkip is a concept the repair left alone, and why.
type repairSkip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// repairTarget is one concept with every fix it needs and the content hash read
// when the plan was made: the apply step writes with it as if_match.
type repairTarget struct {
	ID    okf.ConceptID
	Path  string
	Hash  string
	Fixes []*lint.Fix
}

// planRepair runs lint on scope and returns the concepts whose findings of
// check carry a fix, sorted by path, with the hash of each at listing time.
// It reads, never writes.
func planRepair(k *kb.KB, check, scope string) ([]repairTarget, []repairItem, error) {
	findings, err := lint.Run(k, scope, false)
	if err != nil {
		return nil, nil, err
	}
	byPath := map[string]*repairTarget{}
	var items []repairItem
	for _, f := range findings {
		if f.Check != check || f.Fix == nil || f.Artifact {
			continue
		}
		concept := uiFindingConcept(f.Path)
		if concept == "" {
			continue
		}
		t := byPath[f.Path]
		if t == nil {
			cd, err := k.ReadConcept(okf.ConceptID(concept))
			if err != nil {
				continue // vanished since the walk: nothing to repair
			}
			t = &repairTarget{ID: okf.ConceptID(concept), Path: f.Path, Hash: cd.ContentHash}
			byPath[f.Path] = t
		}
		t.Fixes = append(t.Fixes, f.Fix)
		items = append(items, repairItem{Path: f.Path, Fix: f.Fix})
	}
	targets := make([]repairTarget, 0, len(byPath))
	for _, t := range byPath {
		targets = append(targets, *t)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Path < targets[j].Path })
	sort.SliceStable(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return targets, items, nil
}

// applyRepair writes each target through k.RepairConcept (D356: a page may stay
// as invalid as it was, never worse) with the hash read at planning time as
// if_match: a concept that changed since is skipped, never overwritten. A move
// (nonslug_file_name) goes through concept_move's code path instead. It returns the targets written and the skipped ones. The caller
// holds the KB lock (gitWrap).
//
// mutualGuard (reciprocal_link_item, D309) keeps at most one side of a mutual
// pair: an item A→B is not dropped when this run already dropped B→A, or the
// edge would leave the graph. The lint suppresses such pairs already; this is
// defence-in-depth against a regression there.
func applyRepair(k *kb.KB, targets []repairTarget, mutualGuard bool) (applied []repairTarget, skipped []repairSkip) {
	removed := map[[2]okf.ConceptID]bool{} // (source, target) items dropped in this run
	for _, t := range targets {
		var dropped []okf.ConceptID
		if mutualGuard {
			var fixes []*lint.Fix
			for _, fx := range t.Fixes {
				target := linkItemTarget(t.Path, fx)
				if target != "" && removed[[2]okf.ConceptID{target, t.ID}] {
					skipped = append(skipped, repairSkip{t.Path, fmt.Sprintf("%s: mutual pair: other side already removed", fx.Field)})
					continue
				}
				if target != "" {
					dropped = append(dropped, target)
				}
				fixes = append(fixes, fx)
			}
			if len(fixes) == 0 {
				continue
			}
			t.Fixes = fixes
		}
		cd, err := k.ReadConcept(t.ID)
		if err != nil {
			skipped = append(skipped, repairSkip{t.Path, err.Error()})
			continue
		}
		if len(t.Fixes) > 0 && t.Fixes[0].Kind == lint.FixMove {
			if reason := applyMoveRepair(k, t, cd.ContentHash); reason != "" {
				skipped = append(skipped, repairSkip{t.Path, reason})
				continue
			}
			applied = append(applied, t)
			continue
		}
		fm, _, err := parseForRepair(cd.FrontmatterRaw, hasFixKind(t.Fixes, lint.FixQuoteValue))
		if err != nil {
			skipped = append(skipped, repairSkip{t.Path, "unreadable frontmatter: " + err.Error()})
			continue
		}
		body := cd.Body
		changed, partial, reason := applyFixes(t.ID, fm, &body, t.Fixes)
		if reason == "" && changed == 0 && len(partial) > 0 {
			reason = strings.Join(partial, "; ")
			partial = nil
		}
		if reason != "" {
			skipped = append(skipped, repairSkip{t.Path, reason})
			continue
		}
		// A fix that needs a person does not hold back the others on the
		// same concept: they are written, and it is reported on its own.
		for _, p := range partial {
			skipped = append(skipped, repairSkip{t.Path, p})
		}
		if _, err := k.RepairConcept(t.ID, fm, body, t.Hash); err != nil {
			reason := err.Error()
			if errors.Is(err, okf.ErrStaleWrite) {
				reason = "stale_write: the concept changed since it was listed"
			}
			skipped = append(skipped, repairSkip{t.Path, reason})
			continue
		}
		for _, target := range dropped {
			removed[[2]okf.ConceptID{t.ID, target}] = true
		}
		applied = append(applied, t)
	}
	return applied, skipped
}

// parseForRepair parses a raw frontmatter block for a repair. A block that does
// not parse is repaired in memory when quote is set and the answer is unique
// (lint.QuoteBrokenValue, unparseable_frontmatter): quoted reports it. A page
// without frontmatter has an empty raw block, which parses to an empty
// Frontmatter: add_frontmatter fills it.
func parseForRepair(raw string, quote bool) (fm *okf.Frontmatter, quoted bool, err error) {
	fm, err = okf.ParseFrontmatter(raw)
	if err == nil || !quote {
		return fm, false, err
	}
	fixed, _, ok := lint.QuoteBrokenValue(raw)
	if !ok {
		return nil, false, err
	}
	fm, err = okf.ParseFrontmatter(fixed)
	return fm, err == nil, err
}

func hasFixKind(fixes []*lint.Fix, kind string) bool {
	for _, fx := range fixes {
		if fx.Kind == kind {
			return true
		}
	}
	return false
}

// applyMoveRepair renames a concept to its slug through the concept_move code
// path, so inbound links are rewritten (nonslug_file_name, D356). hash is the
// content hash read now; the plan's own hash must still match. It returns the
// reason the move was not made, or "".
func applyMoveRepair(k *kb.KB, t repairTarget, hash string) string {
	if t.Hash != "" && t.Hash != hash {
		return "stale_write: the concept changed since it was listed"
	}
	fx := t.Fixes[0]
	_, errRes := applyConceptMoves(k, []conceptMoveEntry{{SourceID: fx.Field, TargetID: fx.To}}, true,
		moveOptions{LogTitle: "kb_repair: nonslug_file_name"})
	if errRes != nil {
		if len(errRes.Content) > 0 {
			return errRes.Content[0].Text
		}
		return "move failed"
	}
	return ""
}

// linkItemTarget is the concept a drop_link_item fix unlinks, or "" for any
// other fix. The item is resolved against the ID-derived path: a relative
// markdown link in an expanded concept may not resolve, and the guard then
// simply does not apply to it — the lint check is the primary protection.
func linkItemTarget(path string, fx *lint.Fix) okf.ConceptID {
	if fx.Kind != lint.FixDropLinkItem {
		return ""
	}
	if ids := kb.ExtractLinks(fx.Field, path); len(ids) == 1 {
		return ids[0]
	}
	return ""
}

// applyFixes applies fixes to fm and body in place. It returns how many
// fixes it applied, the fixes it left for a person (partial: the others still
// apply), and a non-empty fatal reason when the concept must not be written.
func applyFixes(id okf.ConceptID, fm *okf.Frontmatter, body *string, fixes []*lint.Fix) (changed int, partial []string, fatal string) {
	handled, renamed, partial := applyRenameGroups(fm, fixes)
	changed += renamed
	changed += applyPrefixReplacements(body, fixes, handled)
	for _, fx := range fixes {
		if handled[fx] {
			continue
		}
		changed++
		switch fx.Kind {
		case lint.FixRenameField, lint.FixReplacePrefix:
			// Never reached: applyRenameGroups decides every rename,
			// applyPrefixReplacements every prefix rewrite.
			changed--
		case lint.FixDropField:
			fm.Delete(fx.Field)
		case lint.FixRebaseLink:
			// Replace the old href with the new one in the body.
			*body = rebaseHrefInBody(*body, fx.Field, fx.To)
		case lint.FixRewriteWikiLink:
			*body = rewriteWikiLinkInBody(*body, fx.Field, fx.To)
		case lint.FixSetValue:
			fm.Set(fx.Field, fx.To)
		case lint.FixQuoteValue:
			// Done when the block was parsed (parseForRepair): the fix only
			// makes the page be written.
		case lint.FixAddFrontmatter:
			if fm.Type() != "" {
				changed-- // already has a type: idempotent
				continue
			}
			fm.Set("type", fx.To)
			if _, has := fm.Get("title"); !has {
				fm.Set("title", lint.DeriveTitle(*body, id))
			}
		case lint.FixSplitValue:
			raw, ok := fm.Get(fx.Field)
			v, isStr := raw.(string)
			if !ok || !isStr {
				continue
			}
			_, rest, prose := lint.SplitProse(v)
			if !prose {
				continue // already split: idempotent
			}
			fm.Set(fx.Field, fx.To)
			*body = insertAfterH1(*body, "> "+fx.Field+": "+rest)
		case lint.FixListifyField:
			raw, ok := fm.Get(fx.Field)
			v, isStr := raw.(string)
			if !ok || !isStr {
				changed-- // already a list: idempotent
				continue
			}
			items := lint.ListItems(fx.Field, v)
			if len(items) == 0 {
				partial = append(partial, fx.Field+": no items to extract from the string")
				changed--
				continue
			}
			fm.Set(fx.Field, items)
		case lint.FixSyncH1:
			nb, ok := replaceFirstH1(*body, fx.To)
			if !ok {
				changed-- // no heading, or already equal: idempotent
				continue
			}
			*body = nb
		case lint.FixUnlinkRepeat:
			nb, ok := lint.UnlinkRepeats(*body, fx.Field)
			if !ok {
				changed-- // already unlinked: idempotent
				continue
			}
			*body = nb
		case lint.FixDropLinkItem:
			*body = lint.DropLinkItem(*body, fx.Field)
		case lint.FixRewriteLinkItem:
			*body = lint.ReplaceLinkItem(*body, fx.Field, fx.To)
		default:
			return 0, nil, "unknown fix kind " + fx.Kind
		}
	}
	return changed, partial, ""
}

// applyPrefixReplacements applies every replace_prefix fix of one concept in
// a single pass, longest prefix first (D316): applied one after the other,
// "wiki/" could rewrite the text "wiki/ops/" was declared for, or rewrite what
// an earlier replacement produced. It marks the fixes it applied in handled
// and returns how many.
func applyPrefixReplacements(body *string, fixes []*lint.Fix, handled map[*lint.Fix]bool) int {
	var prefixes []*lint.Fix
	for _, fx := range fixes {
		if fx.Kind == lint.FixReplacePrefix && fx.Field != "" {
			prefixes = append(prefixes, fx)
			handled[fx] = true
		}
	}
	if len(prefixes) == 0 {
		return 0
	}
	sort.SliceStable(prefixes, func(i, j int) bool { return len(prefixes[i].Field) > len(prefixes[j].Field) })
	pairs := make([]string, 0, 2*len(prefixes))
	for _, fx := range prefixes {
		pairs = append(pairs, fx.Field, fx.To)
	}
	// strings.Replacer tries the pairs in argument order at each position and
	// never rescans its own output: longest first, one pass.
	*body = strings.NewReplacer(pairs...).Replace(*body)
	return len(prefixes)
}

// artifactRepairTarget is one artifact file with every fix it needs and the
// sha256 read when the plan was made.
type artifactRepairTarget struct {
	Path  string
	Hash  string
	Fixes []*lint.Fix
}

// planArtifactRepair is planRepair for an artifact check. Artifact findings
// exist only in a whole-KB lint, so a scoped plan is empty.
func planArtifactRepair(k *kb.KB, check, scope string) ([]artifactRepairTarget, []repairItem, error) {
	findings, err := lint.Run(k, scope, false)
	if err != nil {
		return nil, nil, err
	}
	byPath := map[string]*artifactRepairTarget{}
	var items []repairItem
	for _, f := range findings {
		if f.Check != check || f.Fix == nil || !f.Artifact {
			continue
		}
		t := byPath[f.Path]
		if t == nil {
			abs, rerr := k.ResolveRootPath(f.Path)
			if rerr != nil {
				continue
			}
			data, rerr := os.ReadFile(abs)
			if rerr != nil {
				continue // vanished since the lint: nothing to repair
			}
			t = &artifactRepairTarget{Path: f.Path, Hash: sha256Hex(data)}
			byPath[f.Path] = t
		}
		t.Fixes = append(t.Fixes, f.Fix)
		items = append(items, repairItem{Path: f.Path, Fix: f.Fix})
	}
	targets := make([]artifactRepairTarget, 0, len(byPath))
	for _, t := range byPath {
		targets = append(targets, *t)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Path < targets[j].Path })
	sort.SliceStable(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return targets, items, nil
}

// applyArtifactRepair rewrites each artifact file with its fixes, under the
// same rules as artifact_write: no symlink on the path, the content hash read
// at planning time as if_match, the per-kind validation, the file mode kept.
// The caller holds the KB lock (gitWrap).
func applyArtifactRepair(k *kb.KB, targets []artifactRepairTarget) (applied []artifactRepairTarget, skipped []repairSkip) {
	for _, t := range targets {
		info, err := classifyArtifactPath(t.Path)
		if err != nil {
			skipped = append(skipped, repairSkip{t.Path, err.Error()})
			continue
		}
		if err := rejectArtifactSymlinks(k.Root, t.Path); err != nil {
			skipped = append(skipped, repairSkip{t.Path, err.Error()})
			continue
		}
		abs, err := k.ResolveRootPath(t.Path)
		if err != nil {
			skipped = append(skipped, repairSkip{t.Path, err.Error()})
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			skipped = append(skipped, repairSkip{t.Path, err.Error()})
			continue
		}
		if sha256Hex(data) != t.Hash {
			skipped = append(skipped, repairSkip{t.Path, "stale_write: the file changed since it was listed"})
			continue
		}
		text := string(data)
		for _, fx := range t.Fixes {
			if fx.Kind != lint.FixStripToolPrefix || fx.Field == "" {
				continue
			}
			re := regexp.MustCompile(`\b` + regexp.QuoteMeta(fx.Field) + `\b`)
			text = re.ReplaceAllLiteralString(text, fx.To)
		}
		if text == string(data) {
			skipped = append(skipped, repairSkip{t.Path, "nothing to rewrite"})
			continue
		}
		if err := validateArtifactContent(info, t.Path, []byte(text)); err != nil {
			skipped = append(skipped, repairSkip{t.Path, "the rewritten file would not validate: " + err.Error()})
			continue
		}
		st, err := os.Stat(abs)
		if err != nil {
			skipped = append(skipped, repairSkip{t.Path, err.Error()})
			continue
		}
		if err := os.WriteFile(abs, []byte(text), st.Mode().Perm()); err != nil {
			skipped = append(skipped, repairSkip{t.Path, err.Error()})
			continue
		}
		applied = append(applied, t)
	}
	return applied, skipped
}

// mapRepairTarget is one data/ folder with the title its descriptor gets.
type mapRepairTarget struct {
	Folder, Title string
}

// planMapRepair is planRepair for a check whose findings name a data/ folder
// (unmapped_folder, D357). It reads, never writes.
func planMapRepair(k *kb.KB, check, scope string) ([]mapRepairTarget, []repairItem, error) {
	findings, err := lint.Run(k, scope, false)
	if err != nil {
		return nil, nil, err
	}
	var targets []mapRepairTarget
	var items []repairItem
	for _, f := range findings {
		if f.Check != check || f.Fix == nil || f.Fix.Kind != lint.FixScaffoldMap {
			continue
		}
		targets = append(targets, mapRepairTarget{Folder: f.Fix.Field, Title: f.Fix.To})
		items = append(items, repairItem{Path: f.Path, Fix: f.Fix})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Folder < targets[j].Folder })
	sort.SliceStable(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return targets, items, nil
}

// applyMapRepair writes the descriptor of each folder through the code map_create
// uses (kb.ScaffoldMap). A folder that gained a descriptor since the plan, or
// vanished, is skipped. The caller holds the KB lock (gitWrap).
func applyMapRepair(k *kb.KB, targets []mapRepairTarget) (applied []mapRepairTarget, skipped []repairSkip) {
	for _, t := range targets {
		if err := k.ScaffoldMap(t.Folder, t.Title); err != nil {
			skipped = append(skipped, repairSkip{t.Folder, err.Error()})
			continue
		}
		applied = append(applied, t)
	}
	return applied, skipped
}

// kbRepairMaps is kb_repair for a map check: the same response shape, counted
// in folders.
func kbRepairMaps(k *kb.KB, check, scope string, dryRun bool, limit int) (ToolResult, error) {
	targets, items, err := planMapRepair(k, check, scope)
	if err != nil {
		return errorResult(fmt.Sprintf("kb_repair: %v", err)), nil
	}
	foundTotal := len(items)
	if limit > 0 && len(targets) > limit {
		targets = targets[:limit]
		keep := map[string]bool{}
		for _, t := range targets {
			keep[t.Folder] = true
		}
		kept := items[:0]
		for _, it := range items {
			if keep[it.Path] {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	planned := items
	if len(planned) > repairPlannedCap {
		planned = planned[:repairPlannedCap]
	}
	result := map[string]interface{}{
		"check":         check,
		"dry_run":       dryRun,
		"planned":       planned,
		"planned_total": len(items),
		"found_total":   foundTotal,
		"found_folders": foundTotal,
		"applied":       0,
		"skipped":       []repairSkip{},
	}
	res := ToolResult{}
	if !dryRun && len(targets) > 0 {
		applied, skipped := applyMapRepair(k, targets)
		if len(applied) > 0 {
			entry := fmt.Sprintf("kb_repair: %s (%d folders)\n\n%d applied, %d skipped", check, len(applied), len(applied), len(skipped))
			_ = k.AppendLog(entry, time.Now())
			res.CommitSubject = fmt.Sprintf("kb_repair: %s (%d folders)", check, len(applied))
		}
		result["applied"] = len(applied)
		delete(result, "planned")
		if skipped != nil {
			result["skipped"] = skipped
		}
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	res.Content = textResult(string(out)).Content
	return res, nil
}

// kbRepairArtifacts is kb_repair for an artifact check: the same response
// shape, counted in files instead of concepts.
func kbRepairArtifacts(k *kb.KB, check, scope string, dryRun bool, limit int) (ToolResult, error) {
	if !dryRun && !k.AllowArtifactWrite {
		return errorResult(fmt.Sprintf("kb_repair: %s rewrites artifact files, which needs kbs[].allow_artifact_write for this KB; dry_run still lists the plan", check)), nil
	}
	targets, items, err := planArtifactRepair(k, check, scope)
	if err != nil {
		return errorResult(fmt.Sprintf("kb_repair: %v", err)), nil
	}
	foundTotal, foundFiles := len(items), len(targets)
	if limit > 0 && len(targets) > limit {
		targets = targets[:limit]
		keep := map[string]bool{}
		for _, t := range targets {
			keep[t.Path] = true
		}
		kept := items[:0]
		for _, it := range items {
			if keep[it.Path] {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	planned := items
	if len(planned) > repairPlannedCap {
		planned = planned[:repairPlannedCap]
	}
	result := map[string]interface{}{
		"check":         check,
		"dry_run":       dryRun,
		"planned":       planned,
		"planned_total": len(items),
		"found_total":   foundTotal,
		"found_files":   foundFiles,
		"applied":       0,
		"skipped":       []repairSkip{},
	}
	res := ToolResult{}
	if !dryRun && len(targets) > 0 {
		applied, skipped := applyArtifactRepair(k, targets)
		if len(applied) > 0 {
			entry := fmt.Sprintf("kb_repair: %s (%d files)\n\n%d applied, %d skipped", check, len(applied), len(applied), len(skipped))
			_ = k.AppendLog(entry, time.Now())
			res.CommitSubject = fmt.Sprintf("kb_repair: %s (%d files)", check, len(applied))
		}
		result["applied"] = len(applied)
		delete(result, "planned") // applied/skipped say what happened; the plan is a dry run's product (D318)
		if skipped != nil {
			result["skipped"] = skipped
		}
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	res.Content = textResult(string(out)).Content
	return res, nil
}

// applyRenameGroups applies the rename_field fixes grouped by their target:
// several synonyms of one standard field (date and updated both meaning
// timestamp) are one decision, not a rename followed by a collision with
// itself. When every present value agrees, one is renamed and the rest
// dropped; when they differ, the group is left for a person and named in
// full. handled marks every rename fix it decided.
func applyRenameGroups(fm *okf.Frontmatter, fixes []*lint.Fix) (handled map[*lint.Fix]bool, changed int, partial []string) {
	handled = map[*lint.Fix]bool{}
	var order []string
	groups := map[string][]*lint.Fix{}
	for _, fx := range fixes {
		if fx.Kind != lint.FixRenameField {
			continue
		}
		handled[fx] = true
		if _, ok := fm.Get(fx.Field); !ok {
			continue // already gone: idempotent
		}
		if _, seen := groups[fx.To]; !seen {
			order = append(order, fx.To)
		}
		groups[fx.To] = append(groups[fx.To], fx)
	}
	for _, to := range order {
		group := groups[to]
		var names []string
		var values []string
		for _, fx := range group {
			v, _ := fm.Get(fx.Field)
			names = append(names, fmt.Sprintf("%q", fx.Field))
			values = append(values, fmt.Sprint(v))
		}
		existing, hasTo := fm.Get(to)
		if hasTo {
			values = append(values, fmt.Sprint(existing))
		}
		agree := true
		for _, v := range values[1:] {
			agree = agree && v == values[0]
		}
		if !agree {
			if hasTo {
				partial = append(partial, fmt.Sprintf("%s and the existing %q hold different values: merge them into %q by hand", strings.Join(names, ", "), to, to))
			} else {
				partial = append(partial, fmt.Sprintf("%s all mean %q and hold different values: keep one as %q by hand", strings.Join(names, ", "), to, to))
			}
			continue
		}
		for i, fx := range group {
			if i == 0 && !hasTo {
				fm.Rename(fx.Field, to)
			} else {
				fm.Delete(fx.Field)
			}
			changed++
		}
	}
	return handled, changed, partial
}

// insertAfterH1 inserts line as its own paragraph after the body's first H1,
// or at the top when there is none (D296 split_value: the prose that leaves a
// vocabulary field is kept in the body, never dropped).
func insertAfterH1(body, line string) string {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "# ") {
			rest := append([]string{l, "", line}, lines[i+1:]...)
			return strings.Join(append(lines[:i:i], rest...), "\n")
		}
	}
	return line + "\n\n" + body
}

// replaceFirstH1 overwrites the body's first level-1 heading with `# title`
// (sync_h1, D315). The title is plain text: any formatting the old heading
// carried (bold, a link, a code span) goes with it. A "# " line inside a
// code fence is never touched. ok is false when there is no heading or it
// already reads title.
func replaceFirstH1(body, title string) (string, bool) {
	lines := strings.Split(body, "\n")
	inFence := false
	for i, l := range lines {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(l, "# ") {
			continue
		}
		cr := ""
		if strings.HasSuffix(l, "\r") {
			cr = "\r"
		}
		if strings.TrimSpace(l) == "# "+title {
			return body, false
		}
		lines[i] = "# " + title + cr
		return strings.Join(lines, "\n"), true
	}
	return body, false
}

// rebaseHrefInBody replaces all occurrences of oldHref with newHref inside
// markdown link parentheses — [text](oldHref) → [text](newHref) — leaving
// non-link occurrences untouched. An empty newHref drops the link and keeps
// its label (a link to the concept itself, D310). Idempotent: if the oldHref
// is absent, the body is returned unchanged.
func rebaseHrefInBody(body, oldHref, newHref string) string {
	// Match markdown links whose href is exactly oldHref (possibly with a fragment).
	var sb strings.Builder
	remainder := body
	needle := "](" + oldHref
	for {
		idx := strings.Index(remainder, needle)
		if idx == -1 {
			break
		}
		after := remainder[idx+len(needle):]
		// The character after the href must be ")" or "#" (fragment).
		if len(after) > 0 && after[0] != ')' && after[0] != '#' {
			sb.WriteString(remainder[:idx+len(needle)])
			remainder = after
			continue
		}
		if newHref == "" {
			open := strings.LastIndex(remainder[:idx], "[")
			end := strings.Index(after, ")")
			if open < 0 || end < 0 || strings.Contains(remainder[open:idx], "]") {
				sb.WriteString(remainder[:idx+len(needle)])
				remainder = after
				continue
			}
			sb.WriteString(remainder[:open])
			sb.WriteString(remainder[open+1 : idx])
			remainder = after[end+1:]
			continue
		}
		sb.WriteString(remainder[:idx])
		sb.WriteString("](")
		sb.WriteString(newHref)
		remainder = after
	}
	sb.WriteString(remainder)
	return sb.String()
}

// rewriteWikiLinkInBody replaces the ID of every wiki-link [[old]], [[old#a]],
// [[old|t]] with newID, keeping the anchor and the alias. Code spans are left
// alone, as the link extraction does (D150). Idempotent.
func rewriteWikiLinkInBody(body, oldID, newID string) string {
	masked := kb.MaskCodeSpans(body)
	var sb strings.Builder
	last := 0
	for _, m := range wikiLinkRewriteRe.FindAllStringSubmatchIndex(masked, -1) {
		if masked[m[2]:m[3]] != oldID {
			continue
		}
		sb.WriteString(body[last:m[2]])
		sb.WriteString(newID)
		last = m[3]
	}
	sb.WriteString(body[last:])
	return sb.String()
}

var wikiLinkRewriteRe = regexp.MustCompile(`\[\[([^\[\]|#]+)(#[^\[\]|]*)?(\|[^\[\]]*)?\]\]`)

// renameInContracts rewrites the _map.md of every map in which a renamed field
// is named by required_fields or field_values, so the contract keeps pointing
// at the key the concepts now carry (D289). Only the renamed names change; it
// returns the maps rewritten.
func renameInContracts(k *kb.KB, applied []repairTarget) ([]string, error) {
	renames := map[string]map[string]string{} // map → old → new
	for _, t := range applied {
		mapName, _, ok := strings.Cut(string(t.ID), "/")
		if !ok {
			continue
		}
		for _, fx := range t.Fixes {
			if fx.Kind != lint.FixRenameField {
				continue
			}
			if renames[mapName] == nil {
				renames[mapName] = map[string]string{}
			}
			renames[mapName][fx.Field] = fx.To
		}
	}
	names := make([]string, 0, len(renames))
	for n := range renames {
		names = append(names, n)
	}
	sort.Strings(names)
	var rewritten []string
	for _, name := range names {
		c, err := k.ReadMapContract(name)
		if err != nil {
			continue // no readable descriptor: nothing to keep in step
		}
		upd, changed := contractRenameUpdate(c, renames[name])
		if !changed {
			continue
		}
		if _, err := k.UpdateMapContract(name, upd); err != nil {
			return rewritten, fmt.Errorf("map %s: %w", name, err)
		}
		rewritten = append(rewritten, name)
	}
	return rewritten, nil
}

// contractRenameUpdate builds the MapContractUpdate that renames fields in the
// parts of c that name them, leaving the other parts nil (untouched).
func contractRenameUpdate(c kb.MapContract, ren map[string]string) (kb.MapContractUpdate, bool) {
	var upd kb.MapContractUpdate
	changed := false
	renameList := func(in []string) ([]string, bool) {
		out := make([]string, len(in))
		hit := false
		for i, f := range in {
			if to, ok := ren[f]; ok {
				out[i], hit = to, true
			} else {
				out[i] = f
			}
		}
		return out, hit
	}
	renameValues := func(in map[string][]string) (map[string][]string, bool) {
		out := map[string][]string{}
		hit := false
		for f, v := range in {
			if to, ok := ren[f]; ok {
				f, hit = to, true
			}
			out[f] = append(out[f], v...)
		}
		return out, hit
	}
	if l, hit := renameList(c.RequiredFields); hit {
		upd.RequiredFields, changed = &l, true
	}
	byType := map[string][]string{}
	hitType := false
	for typ, l := range c.RequiredFieldsByType {
		nl, hit := renameList(l)
		byType[typ] = nl
		hitType = hitType || hit
	}
	if hitType {
		upd.RequiredFieldsByType, changed = byType, true
	}
	if v, hit := renameValues(c.FieldValues); hit {
		upd.FieldValues, changed = v, true
	}
	fvt := map[string]map[string][]string{}
	hitFV := false
	for typ, m := range c.FieldValuesByType {
		nm, hit := renameValues(m)
		fvt[typ] = nm
		hitFV = hitFV || hit
	}
	if hitFV {
		upd.FieldValuesByType, changed = fvt, true
	}
	return upd, changed
}

func toolKBRepair(k *kb.KB) Tool {
	return Tool{
		Name: "kb_repair",
		// The fixable checks are not listed here: a finding carries its fix,
		// and an unknown check is refused naming them (D301 budget).
		Description: "Applies the mechanical fix a lint finding carries (fix) for one check, " +
			"KB-wide in one commit. dry_run defaults to true (plan only). " +
			"Concepts changed since listing are skipped. Judgement fixes: kb-doctor skill.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["check"],
			"properties": {
				"check": {"type": "string"},
				"scope": {"type": "string"},
				"dry_run": {"type": "boolean", "description": "Default true"},
				"limit": {"type": "integer"}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Check  string `json:"check"`
				Scope  string `json:"scope"`
				DryRun *bool  `json:"dry_run"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			fixable := false
			for _, c := range lint.FixableChecks {
				fixable = fixable || c == params.Check
			}
			if !fixable {
				return errorResult(fmt.Sprintf("kb_repair: check %q emits no mechanical fix (unknown, or judgement-only: see its findings with the lint tool); checks that emit fixes: %s",
					params.Check, strings.Join(lint.FixableChecks, ", "))), nil
			}
			dryRun := params.DryRun == nil || *params.DryRun
			if lint.ArtifactRepairCheck(params.Check) {
				return kbRepairArtifacts(k, params.Check, params.Scope, dryRun, params.Limit)
			}
			if lint.MapRepairCheck(params.Check) {
				return kbRepairMaps(k, params.Check, params.Scope, dryRun, params.Limit)
			}

			targets, items, err := planRepair(k, params.Check, params.Scope)
			if err != nil {
				return errorResult(fmt.Sprintf("kb_repair: %v", err)), nil
			}
			// The whole job, before limit: a dry run with a limit must still
			// say how much work there is, not only the page it shows.
			foundTotal, foundConcepts := len(items), len(targets)
			if params.Limit > 0 && len(targets) > params.Limit {
				targets = targets[:params.Limit]
				keep := map[string]bool{}
				for _, t := range targets {
					keep[t.Path] = true
				}
				kept := items[:0]
				for _, it := range items {
					if keep[it.Path] {
						kept = append(kept, it)
					}
				}
				items = kept
			}

			planned := items
			if len(planned) > repairPlannedCap {
				planned = planned[:repairPlannedCap]
			}
			result := map[string]interface{}{
				"check":         params.Check,
				"dry_run":       dryRun,
				"planned":       planned,
				"planned_total": len(items),
				// found_*: in scope before limit; equal to planned_* without one.
				"found_total":    foundTotal,
				"found_concepts": foundConcepts,
				"applied":        0,
				"skipped":        []repairSkip{},
			}
			res := ToolResult{}
			if !dryRun && len(targets) > 0 {
				applied, skipped := applyRepair(k, targets, params.Check == "reciprocal_link_item")
				var rewritten []string
				if len(applied) > 0 {
					var cerr error
					rewritten, cerr = renameInContracts(k, applied)
					if cerr != nil {
						return errorResult(fmt.Sprintf("kb_repair: contract rewrite failed after %d concept(s) were written (uncommitted, revert or re-run): %v", len(applied), cerr)), nil
					}
					entry := fmt.Sprintf("kb_repair: %s (%d concepts)\n\n%d applied, %d skipped, %d map contract(s) rewritten",
						params.Check, len(applied), len(applied), len(skipped), len(rewritten))
					_ = k.AppendLog(entry, time.Now())
					res.CommitSubject = fmt.Sprintf("kb_repair: %s (%d concepts)", params.Check, len(applied))
				}
				result["applied"] = len(applied)
				delete(result, "planned") // applied/skipped say what happened; the plan is a dry run's product (D318)
				if skipped != nil {
					result["skipped"] = skipped
				}
				if len(rewritten) > 0 {
					result["contracts_rewritten"] = rewritten
				}
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			res.Content = textResult(string(out)).Content
			return res, nil
		},
	}
}

// fixpointCheck evaluates the rewritten content of a concept on every pass but
// the first; a variable so a test can feed it synthetic fixes (a cycle cannot
// be built from the real checks).
var fixpointCheck = lint.CheckConcept

// repairFixpointMax bounds the apply passes of repairConceptFixpoint (D355):
// real chains are three or four links long (rename a field, split its prose,
// normalise the value), so more than this is a cycle, not a long chain.
const repairFixpointMax = 8

// fixpointOutcome is what repairConceptFixpoint did to one concept.
type fixpointOutcome struct {
	// Changed counts, per check, the fixes applied across all passes.
	Changed map[string]int
	// Renames are the rename_field fixes that took effect, for the map
	// contracts that name the renamed field (renameInContracts).
	Renames []*lint.Fix
	// Stuck lists the checks left with a finding the applier could not fix on
	// its own (a person's call, or a fix that does nothing): reported once.
	Stuck []string
	// Notes says why, one line per stuck check.
	Notes []string
	// Hash is the content hash after the write ("" when nothing was written).
	Hash string
}

// conceptContent is the file text WriteConcept produces for fm and body: what
// lint.CheckConcept must be fed to judge a rewrite before it is written.
func conceptContent(fm *okf.Frontmatter, body string) string {
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return "---\n" + fm.Serialize() + "\n---\n" + body
}

// repairConceptFixpoint applies the allowed checks' mechanical fixes to one
// concept until none is left (D355). Deterministic repairs depend on each
// other (a rename exposes a prose value, a split exposes an invalid value), so
// one pass converges a KB only over several runs; here the loop runs on the
// in-memory content and the concept is written once, whatever the number of
// passes.
//
// Pass 0 uses seed (the findings the caller already has, which can include
// graph-level checks such as duplicate_link that CheckConcept does not
// compute); later passes re-evaluate the rewritten content with
// lint.CheckConcept. Cross-concept checks, artifact findings and checks outside
// allowed are never applied here: stage 2 of the heartbeat owns them. allowed
// is also the order checks are applied in within a pass.
//
// At most repairFixpointMax passes; a content seen twice is an oscillation.
// Both write nothing and return a skip naming the checks. The caller holds the
// KB lock (gitWrap).
func repairConceptFixpoint(k *kb.KB, id okf.ConceptID, allowed []string, seed []lint.Finding) (fixpointOutcome, *repairSkip) {
	out := fixpointOutcome{Changed: map[string]int{}}
	path := okf.IDToPath(id)
	skip := func(reason string) (fixpointOutcome, *repairSkip) {
		return fixpointOutcome{Changed: map[string]int{}}, &repairSkip{Path: path, Reason: reason}
	}
	allow := map[string]bool{}
	for _, c := range allowed {
		allow[c] = true
	}
	cd, err := k.ReadConcept(id)
	if err != nil {
		return skip(err.Error())
	}
	fm, quoted, err := parseForRepair(cd.FrontmatterRaw, allow["unparseable_frontmatter"])
	if err != nil {
		return skip("unreadable frontmatter: " + err.Error())
	}
	body := cd.Body
	content := conceptContent(fm, body)
	last := okf.ContentHash(content)
	origHash := last
	if quoted {
		// The block was repaired in memory: what is on disk differs from it
		// even if no other fix changes anything.
		origHash = cd.ContentHash
	}
	seen := map[string]bool{last: true}
	stuck := map[string]bool{}

	for pass := 0; ; pass++ {
		var found []lint.Finding
		if pass == 0 && seed != nil {
			found = seed
		} else {
			found = fixpointCheck(k, id, content)
		}
		byCheck := map[string][]*lint.Fix{}
		for _, f := range found {
			if f.Fix == nil || f.Artifact || !allow[f.Check] || stuck[f.Check] {
				continue
			}
			if s, ok := lint.Spec(f.Check); !ok || s.CrossConcept {
				continue
			}
			if pass == 0 && seed != nil && uiFindingConcept(f.Path) != string(id) {
				continue
			}
			byCheck[f.Check] = append(byCheck[f.Check], f.Fix)
		}
		if len(byCheck) == 0 {
			break
		}
		if pass == repairFixpointMax {
			return skip(nonConvergence(byCheck, id, "did not converge"))
		}
		passChanged := 0
		for _, check := range allowed {
			fixes := byCheck[check]
			if len(fixes) == 0 {
				continue
			}
			savedFM, savedBody := fm.Serialize(), body
			changed, partial, fatal := applyFixes(id, fm, &body, fixes)
			if fatal != "" {
				if restored, perr := okf.ParseFrontmatter(savedFM); perr == nil {
					fm, body = restored, savedBody
				}
				stuck[check] = true
				out.Notes = append(out.Notes, check+": "+fatal)
				continue
			}
			// A fix that needs a person does not hold back the others, and is
			// reported once instead of being retried on every pass.
			if len(partial) > 0 || changed == 0 {
				stuck[check] = true
				for _, p := range partial {
					out.Notes = append(out.Notes, check+": "+p)
				}
			}
			if changed > 0 {
				out.Changed[check] += changed
				passChanged += changed
				for _, fx := range fixes {
					if fx.Kind == lint.FixRenameField {
						out.Renames = append(out.Renames, fx)
					}
				}
			}
		}
		if passChanged == 0 {
			break
		}
		content = conceptContent(fm, body)
		h := okf.ContentHash(content)
		if h == last {
			break // the fixes cancelled out: converged on what was already there
		}
		if seen[h] {
			return skip(nonConvergence(byCheck, id, "did not converge"))
		}
		seen[h], last = true, h
	}
	if quoted && out.Changed["unparseable_frontmatter"] == 0 {
		out.Changed["unparseable_frontmatter"] = 1 // no seed named it, the repair still happened
	}
	for c := range stuck {
		out.Stuck = append(out.Stuck, c)
	}
	sort.Strings(out.Stuck)
	if okf.ContentHash(content) == origHash {
		out.Changed, out.Renames = map[string]int{}, nil
		return out, nil
	}
	hash, err := k.RepairConcept(id, fm, body, cd.ContentHash)
	if err != nil {
		reason := err.Error()
		if errors.Is(err, okf.ErrStaleWrite) {
			reason = "stale_write: the concept changed since it was read"
		}
		return skip(reason)
	}
	out.Hash = hash
	return out, nil
}

// nonConvergence is the skip reason of a concept the fixpoint left alone, and
// the stderr line that tells an operator which fixes keep undoing each other.
func nonConvergence(byCheck map[string][]*lint.Fix, id okf.ConceptID, what string) string {
	names := make([]string, 0, len(byCheck))
	for c := range byCheck {
		names = append(names, c)
	}
	sort.Strings(names)
	reason := what + ": " + strings.Join(names, ", ")
	fmt.Fprintf(os.Stderr, "cartographer: repair %s: %s\n", id, reason)
	return reason
}
