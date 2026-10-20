package kb

import (
	"fmt"
	gopath "path"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// RealignContract rewrites a map's _map.md to the current contract shape
// (D907): it drops every removed key (LegacyContractKeys), moves a flat
// required_fields into required_fields.* and a flat field_values.<field> into
// field_values.*.<field>, and leaves every other key, its comments and the
// body untouched, and renames to its standard name a field the contract names
// under a synonym (ContractFieldCanonical). It reports whether it changed the file. A legacy
// _archive.md descriptor is refused, as UpdateMapContract refuses it.
func (kb *KB) RealignContract(name string) (bool, error) {
	if _, err := okf.PathToID(name + ".md"); err != nil {
		return false, fmt.Errorf("%w: invalid map name %q", okf.ErrInvalidPath, name)
	}
	relPath, err := kb.mapDescriptorRelPath(name)
	if err != nil {
		return false, fmt.Errorf("RealignContract %s: %w", name, err)
	}
	if gopath.Base(relPath) != "_map.md" {
		return false, fmt.Errorf("RealignContract %s: legacy _archive.md descriptor — rewrite it as _map.md with a kind first (D77)", name)
	}
	content, err := kb.ReadRaw(relPath)
	if err != nil {
		return false, fmt.Errorf("RealignContract %s: %w", name, err)
	}
	fmRaw, body, ok := okf.SplitFrontmatter(content)
	if !ok {
		return false, fmt.Errorf("RealignContract %s: missing frontmatter in _map.md", name)
	}
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil {
		return false, fmt.Errorf("RealignContract %s: %w", name, err)
	}
	legacy := LegacyContractKeys(fm)
	if len(legacy) == 0 && len(ContractSynonymKeys(fm)) == 0 {
		return false, nil
	}
	for _, key := range legacy {
		switch {
		case key == "required_fields":
			flat, _ := fm.Get(key)
			list, _ := flat.([]string)
			if cur, ok := fm.Get("required_fields." + AllTypes); ok {
				old, _ := cur.([]string)
				list = append(list, old...)
			}
			fm.Delete(key)
			if list = sortedUnique(list); len(list) > 0 {
				fm.Set("required_fields."+AllTypes, list)
			}
		case strings.HasPrefix(key, "field_values."):
			field := strings.TrimPrefix(key, "field_values.")
			if _, has := fm.Get("field_values." + AllTypes + "." + field); has {
				fm.Delete(key)
			} else {
				fm.Rename(key, "field_values."+AllTypes+"."+field)
			}
		default:
			fm.Delete(key)
		}
	}
	// A required or forbidden field, or a field_values key, that the page
	// repair renames follows the rename (updated → timestamp), so a contract
	// never demands the name the repair removes.
	canonicalizeContractFields(fm)
	abs, err := kb.ResolvePath(relPath, false)
	if err != nil {
		return false, fmt.Errorf("RealignContract %s: %w", name, err)
	}
	out := "---\n" + fm.Serialize() + "\n---\n" + body
	if err := writeFileAtomic(abs, []byte(out)); err != nil {
		return false, fmt.Errorf("RealignContract %s: write _map.md: %w", name, err)
	}
	return true, nil
}

// ContractLoosening lists, in words, what a change from before to after
// loosens (D907): a rule the contract no longer states, or a set it now
// allows more of, so a page that failed before can pass. A tightening, or a
// change that only adds a rule, loosens nothing. The list is sorted and empty
// when the change loosens nothing.
//
// The rules compared: a required field dropped for a type; a forbidden field
// no longer forbidden; a field's allowed values dropped or widened (for the
// `*` entry and for every type either side names); the type list dropped
// (the map is no longer strict) or widened; the templates dropped or widened.
func ContractLoosening(before, after MapContract) []string {
	var out []string

	types := map[string]bool{AllTypes: true}
	for t := range before.RequiredFieldsByType {
		types[t] = true
	}
	for t := range types {
		have := map[string]bool{}
		now := after.RequiredFields
		old := before.RequiredFields
		if t != AllTypes {
			now, old = after.RequiredFor(t), before.RequiredFor(t)
		}
		for _, f := range now {
			have[f] = true
		}
		for _, f := range old {
			if !have[f] {
				out = append(out, fmt.Sprintf("drops required field %q%s", f, forType(t)))
			}
		}
	}

	forbidden := map[string]bool{}
	for _, f := range after.ForbiddenFields {
		forbidden[f] = true
	}
	for _, f := range before.ForbiddenFields {
		if !forbidden[f] {
			out = append(out, fmt.Sprintf("stops forbidding field %q", f))
		}
	}

	valueTypes := map[string]bool{AllTypes: true}
	fields := map[string]bool{}
	for f := range before.FieldValues {
		fields[f] = true
	}
	for t, byField := range before.FieldValuesByType {
		valueTypes[t] = true
		for f := range byField {
			fields[f] = true
		}
	}
	for t := range after.FieldValuesByType {
		valueTypes[t] = true
	}
	for t := range valueTypes {
		for f := range fields {
			oldVals, oldOK := before.AllowedValues(t, f)
			if !oldOK {
				continue
			}
			newVals, newOK := after.AllowedValues(t, f)
			if !newOK {
				out = append(out, fmt.Sprintf("drops the allowed values of field %q%s", f, forType(t)))
				continue
			}
			if extra := notIn(newVals, oldVals); len(extra) > 0 {
				out = append(out, fmt.Sprintf("allows %s for field %q%s", quoteList(extra), f, forType(t)))
			}
		}
	}

	if len(before.ConceptTypes) > 0 {
		if len(after.ConceptTypes) == 0 {
			out = append(out, "drops the concept type list (the map is no longer strict)")
		} else if extra := notIn(after.ConceptTypes, before.ConceptTypes); len(extra) > 0 {
			out = append(out, fmt.Sprintf("allows the concept type(s) %s", quoteList(extra)))
		}
	}

	if before.HasTemplateKeys() {
		if !after.HasTemplateKeys() {
			out = append(out, "drops the templates (pages no longer have to bind to one)")
		} else if extra := notIn(after.Templates, before.Templates); len(extra) > 0 {
			out = append(out, fmt.Sprintf("allows the template(s) %s", quoteList(extra)))
		}
	}

	sort.Strings(out)
	return out
}

func forType(t string) string {
	if t == AllTypes {
		return ""
	}
	return fmt.Sprintf(" for type %q", t)
}

// notIn returns the values of have that are absent from base.
func notIn(have, base []string) []string {
	in := map[string]bool{}
	for _, v := range base {
		in[v] = true
	}
	var out []string
	for _, v := range have {
		if !in[v] {
			out = append(out, v)
		}
	}
	return out
}

func quoteList(vals []string) string {
	q := make([]string, len(vals))
	for i, v := range vals {
		q[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(q, ", ")
}
