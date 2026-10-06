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

// applyRepair writes each target through k.WriteConcept with the hash read at
// planning time as if_match: a concept that changed since is skipped, never
// overwritten. It returns the targets written and the skipped ones. The caller
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
		fm, err := okf.ParseFrontmatter(cd.FrontmatterRaw)
		if err != nil {
			skipped = append(skipped, repairSkip{t.Path, "unreadable frontmatter: " + err.Error()})
			continue
		}
		body := cd.Body
		changed, partial, reason := applyFixes(fm, &body, t.Fixes)
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
		if _, err := k.WriteConcept(t.ID, fm, body, t.Hash); err != nil {
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
func applyFixes(fm *okf.Frontmatter, body *string, fixes []*lint.Fix) (changed int, partial []string, fatal string) {
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
			items := lint.ListItems(v)
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

// artifactRepairChecks are the fixable checks whose findings name a KB-root
// artifact file rather than a concept (D316). Their repair rewrites that file
// the way artifact_write would, so it needs the same per-KB opt-in.
var artifactRepairChecks = map[string]bool{"legacy_tool_name": true}

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
			if artifactRepairChecks[params.Check] {
				return kbRepairArtifacts(k, params.Check, params.Scope, dryRun, params.Limit)
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
