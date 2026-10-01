package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
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
		if f.Check != check || f.Fix == nil {
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
func applyRepair(k *kb.KB, targets []repairTarget) (applied []repairTarget, skipped []repairSkip) {
	for _, t := range targets {
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
		if reason := applyFixes(fm, &body, t.Fixes); reason != "" {
			skipped = append(skipped, repairSkip{t.Path, reason})
			continue
		}
		if _, err := k.WriteConcept(t.ID, fm, body, t.Hash); err != nil {
			reason := err.Error()
			if errors.Is(err, okf.ErrStaleWrite) {
				reason = "stale_write: the concept changed since it was listed"
			}
			skipped = append(skipped, repairSkip{t.Path, reason})
			continue
		}
		applied = append(applied, t)
	}
	return applied, skipped
}

// applyFixes applies fixes to fm and body in place and returns a non-empty
// reason when one cannot be applied (the caller then discards the write).
func applyFixes(fm *okf.Frontmatter, body *string, fixes []*lint.Fix) string {
	for _, fx := range fixes {
		switch fx.Kind {
		case lint.FixRenameField:
			if _, ok := fm.Get(fx.Field); !ok {
				continue // already gone: idempotent
			}
			if !fm.Rename(fx.Field, fx.To) {
				return fmt.Sprintf("%q already exists: merge the values by hand", fx.To)
			}
		case lint.FixDropField:
			fm.Delete(fx.Field)
		case lint.FixRebaseLink:
			// Replace the old href with the new one in the body.
			*body = rebaseHrefInBody(*body, fx.Field, fx.To)
		case lint.FixDropLinkItem:
			*body = lint.DropLinkItem(*body, fx.Field)
		default:
			return "unknown fix kind " + fx.Kind
		}
	}
	return ""
}

// rebaseHrefInBody replaces all occurrences of oldHref with newHref inside
// markdown link parentheses — [text](oldHref) → [text](newHref) — leaving
// non-link occurrences untouched. Idempotent: if the oldHref is absent, the
// body is returned unchanged.
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
		sb.WriteString(remainder[:idx])
		sb.WriteString("](")
		sb.WriteString(newHref)
		remainder = after
	}
	sb.WriteString(remainder)
	return sb.String()
}

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
		Description: "Applies the mechanical fix lint attaches to one check (" + strings.Join(lint.FixableChecks, ", ") +
			"): applies frontmatter or body fixes KB-wide in one commit. dry_run defaults to true (plan only). " +
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

			targets, items, err := planRepair(k, params.Check, params.Scope)
			if err != nil {
				return errorResult(fmt.Sprintf("kb_repair: %v", err)), nil
			}
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
				"applied":       0,
				"skipped":       []repairSkip{},
			}
			res := ToolResult{}
			if !dryRun && len(targets) > 0 {
				applied, skipped := applyRepair(k, targets)
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
