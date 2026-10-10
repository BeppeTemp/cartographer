package mcpserver

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// patchFrontmatter is the frontmatter half of a concept_patch or of a
// concept_batch patch operation (D342). One struct and one apply function
// serve both so the two cannot diverge.
type patchFrontmatter struct {
	Merge  map[string]interface{} // 'frontmatter': shallow merge, null removes
	Append map[string]interface{} // 'frontmatter_append': add list items
	Remove map[string]interface{} // 'frontmatter_remove': drop list items
	Unset  []string               // 'unset': remove keys, applied last
}

// any reports whether the patch asks for a frontmatter change.
func (p patchFrontmatter) any() bool {
	return len(p.Merge) > 0 || len(p.Append) > 0 || len(p.Remove) > 0 || len(p.Unset) > 0
}

// listItems normalizes a frontmatter_append/remove value: a string is a
// one-item list; anything else than an array of non-empty strings is refused.
func listItems(param, key string, val interface{}) ([]string, error) {
	switch v := val.(type) {
	case string:
		if v == "" {
			return nil, fmt.Errorf("invalid params: %s: %s has an empty item", param, key)
		}
		return []string{v}, nil
	case []interface{}:
		items := make([]string, 0, len(v))
		for _, it := range v {
			s, ok := it.(string)
			if !ok || s == "" {
				return nil, fmt.Errorf("invalid params: %s: %s items must be non-empty strings", param, key)
			}
			items = append(items, s)
		}
		return items, nil
	}
	return nil, fmt.Errorf("invalid params: %s: %s must be a string or an array of strings", param, key)
}

// currentList reads a list-valued field: missing, null and empty are an empty
// list, a scalar string a one-item list, a nested block is not a list.
func currentList(fm *okf.Frontmatter, param, key string) ([]string, error) {
	val, _ := fm.Get(key)
	switch v := val.(type) {
	case nil:
		return nil, nil
	case string:
		if v == "" {
			return nil, nil
		}
		return []string{v}, nil
	case []string:
		return append([]string(nil), v...), nil
	}
	return nil, fmt.Errorf("%s: %s is not a list", param, key)
}

// isList reports whether a frontmatter value is a YAML sequence.
func isList(v interface{}) bool {
	_, ok := v.([]string)
	return ok
}

// isListValue reports whether a patch value is a list (as decoded from JSON).
func isListValue(v interface{}) bool {
	switch v.(type) {
	case []string, []interface{}:
		return true
	}
	return false
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// applyPatchFrontmatter applies the merge, then append, then remove, then
// unset (so unset still wins) onto fm. Append and remove are computed on the
// list as parsed from disk inside the write path, never on what the caller
// last saw: that is what a stale copy cannot defeat (D342). Everything is
// validated before fm is touched. It returns the list_items_dropped message
// for a merge that shrank a list without saying so, "" when there is none.
func applyPatchFrontmatter(fm *okf.Frontmatter, p patchFrontmatter) (dropped string, err error) {
	if p.Merge != nil {
		if err := rejectToolParamKeys(p.Merge, true); err != nil {
			return "", err
		}
	}
	adds := map[string][]string{}
	rems := map[string][]string{}
	for _, spec := range []struct {
		param string
		in    map[string]interface{}
		out   map[string][]string
	}{{"frontmatter_append", p.Append, adds}, {"frontmatter_remove", p.Remove, rems}} {
		for _, key := range sortedKeys(spec.in) {
			if key == "type" {
				return "", fmt.Errorf("'%s' cannot change %q: the field is required", spec.param, key)
			}
			items, err := listItems(spec.param, key, spec.in[key])
			if err != nil {
				return "", err
			}
			spec.out[key] = items
		}
	}
	// A tool-parameter key may be removed (the repair), never added.
	if err := rejectToolParamKeys(p.Append, false); err != nil {
		return "", err
	}
	for _, key := range p.Unset {
		if key == "type" {
			return "", fmt.Errorf("'unset' cannot remove %q: the field is required", key)
		}
	}

	// "Before" values of the merged keys, for the list_items_dropped guard.
	// A scalar replaced by a scalar is skipped: currentList reads a scalar as
	// a one-item list (right for append), but a timestamp bump or a new title
	// drops nothing (#685). A scalar turned into a list still counts.
	before := map[string][]string{}
	for key, val := range p.Merge {
		if val == nil {
			continue
		}
		if cur, _ := fm.Get(key); !isList(cur) && !isListValue(val) {
			continue
		}
		if old, err := currentList(fm, "", key); err == nil && len(old) > 0 {
			before[key] = old
		}
	}

	applyFrontmatterMap(fm, p.Merge)

	// Compute every append/remove result before mutating, so a Block value
	// in any key leaves fm untouched.
	results := map[string][]string{}
	order := []string{}
	for _, key := range sortedKeys(p.Append) {
		cur, err := currentList(fm, "frontmatter_append", key)
		if err != nil {
			return "", err
		}
		have := map[string]bool{}
		for _, it := range cur {
			have[it] = true
		}
		for _, it := range adds[key] {
			if !have[it] {
				have[it] = true
				cur = append(cur, it)
			}
		}
		results[key] = cur
		order = append(order, key)
	}
	for _, key := range order {
		fm.Set(key, results[key])
	}
	for _, key := range sortedKeys(p.Remove) {
		cur, err := currentList(fm, "frontmatter_remove", key)
		if err != nil {
			return "", err
		}
		drop := map[string]bool{}
		for _, it := range rems[key] {
			drop[it] = true
		}
		kept := cur[:0:0]
		for _, it := range cur {
			if !drop[it] {
				kept = append(kept, it)
			}
		}
		switch {
		case len(kept) == len(cur):
		case len(kept) == 0:
			fm.Delete(key)
		default:
			fm.Set(key, kept)
		}
	}
	for _, key := range p.Unset {
		fm.Delete(key)
	}

	return droppedMessage(fm, before, p), nil
}

// droppedMessage builds the list_items_dropped message (WP2): for each merged
// key whose old list lost items, in key order. Keys named in unset, remove or
// set to null are explicit removals and never reported.
func droppedMessage(fm *okf.Frontmatter, before map[string][]string, p patchFrontmatter) string {
	skip := map[string]bool{}
	for _, k := range p.Unset {
		skip[k] = true
	}
	for k := range p.Remove {
		skip[k] = true
	}
	var parts []string
	for _, key := range sortedKeys(p.Merge) {
		old := before[key]
		if skip[key] || len(old) == 0 {
			continue
		}
		cur, err := currentList(fm, "", key)
		if err != nil {
			continue
		}
		now := map[string]bool{}
		for _, it := range cur {
			now[it] = true
		}
		var lost []string
		for _, it := range old {
			if !now[it] {
				lost = append(lost, it)
			}
		}
		if len(lost) == 0 {
			continue
		}
		shown := make([]string, 0, 5)
		for _, it := range lost[:min(len(lost), 5)] {
			shown = append(shown, fmt.Sprintf("%q", cutBytes(it, 80)))
		}
		msg := fmt.Sprintf("%s (%d): %s", key, len(lost), strings.Join(shown, ", "))
		if len(lost) > 5 {
			msg += fmt.Sprintf(", +%d more", len(lost)-5)
		}
		parts = append(parts, msg)
	}
	if len(parts) == 0 {
		return ""
	}
	return "list_items_dropped: " + strings.Join(parts, "; ") +
		" — resend with frontmatter_append/frontmatter_remove to change a list without replacing it"
}

// cutBytes shortens s to at most n bytes on a rune boundary.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

// droppedFinding wraps the message as the write-time-only finding. It is not a
// lint check (lint cannot know the previous state): no conformanceChecks
// entry, no fix, not suppressible by lint_ignore.
func droppedFinding(id, msg string) []findingOut {
	if msg == "" {
		return nil
	}
	return []findingOut{{Path: okf.IDToPath(okf.ConceptID(id)), Check: "list_items_dropped", Severity: "warning", Message: msg}}
}
