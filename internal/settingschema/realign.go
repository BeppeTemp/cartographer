package settingschema

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Removed is a key a release dropped from a settings file (the 0.25 cleanup,
// #903 WP1). Each schema owns its own list, next to it, so the loader can warn
// with the replacement and the realignment can drop the key. A list grows as a
// plan removes a key; nothing here is ever deleted, so an old file keeps being
// understood.
type Removed struct {
	// Path is the dotted key, as in Key.Path. A "[]" segment matches the key
	// inside every entry of that list ("kbs[].git_profile").
	Path string
	// Replacement is one line telling a human what to do instead, named in the
	// load warning and the realignment report. "none: the behaviour is now
	// always on" when the feature it switched is now unconditional.
	Replacement string
	// Plan is the issue number that removed it ("#903"), for the report.
	Plan string
}

// OffReset is a key a release keeps but whose "feature off" value is no longer
// allowed: the realignment resets it to the schema default (every feature on,
// #903 WP1 / P4). Off is the literal YAML value that meant off ("false", "0").
type OffReset struct {
	Path string
	Off  string
}

// RealignReport is what the realignment changed in one file, for the upgrade's
// human-readable summary. Empty (Changed false) means the file was already
// canonical and nothing — not even a backup — was written.
type RealignReport struct {
	Changed     bool
	RemovedKeys []string // dotted paths dropped, with their replacement
	ResetKeys   []string // paths whose off value was reset to the default
	Reformatted bool     // the file's comments/order were rewritten to canonical
}

// Lines is the report as human-readable lines, empty when nothing changed.
func (r RealignReport) Lines() []string {
	var out []string
	for _, k := range r.RemovedKeys {
		out = append(out, "removed "+k)
	}
	for _, k := range r.ResetKeys {
		out = append(out, "reset to default "+k)
	}
	if r.Reformatted && len(out) == 0 {
		out = append(out, "reformatted to the canonical layout")
	}
	return out
}

// removedLookup indexes a removed list by its concrete and its list-wildcard
// path, so both "git.profile" and "kbs[].git_profile" resolve.
func removedLookup(removed []Removed) map[string]Removed {
	m := make(map[string]Removed, len(removed))
	for _, r := range removed {
		m[r.Path] = r
	}
	return m
}

// RemovedKeysIn returns the removed keys the YAML in data still carries, each
// with its replacement, so a loader can warn without rewriting the file. The
// order is the file's, deduplicated, so the warning is stable.
func (s Schema) RemovedKeysIn(data []byte, removed []Removed) []Removed {
	idx := removedLookup(removed)
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var hits []Removed
	walkMapping(doc.Content[0], "", func(path string, _ *yaml.Node) {
		if r, ok := idx[path]; ok && !seen[path] {
			seen[path] = true
			hits = append(hits, r)
		}
	})
	return hits
}

// Realign rewrites data into the canonical commented form, dropping every
// removed key it carries and resetting every off value to the default, and
// reports what it changed (#903 WP1). It is idempotent: a file already
// canonical, with no removed key and no off value, comes back byte-for-byte and
// Changed is false, so the caller writes no backup.
func (s Schema) Realign(data []byte, removed []Removed, offs []OffReset) (out []byte, report RealignReport, err error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, RealignReport{}, err
	}
	m := docMapping(&doc)
	if m == nil {
		// Not a mapping document: nothing to realign, leave it untouched.
		return data, RealignReport{}, nil
	}

	idx := removedLookup(removed)
	report.RemovedKeys = dropRemoved(m, "", idx)

	offIdx := make(map[string]string, len(offs))
	for _, o := range offs {
		offIdx[o.Path] = o.Off
	}
	report.ResetKeys = resetOff(m, "", offIdx, s)

	s.Render(&doc)
	out, err = Encode(&doc)
	if err != nil {
		return nil, RealignReport{}, err
	}

	report.Reformatted = !equalYAML(data, out)
	report.RemovedKeys = dedupSorted(report.RemovedKeys)
	report.ResetKeys = dedupSorted(report.ResetKeys)
	report.Changed = len(report.RemovedKeys) > 0 || len(report.ResetKeys) > 0 || report.Reformatted
	if !report.Changed {
		// Hand back the exact input, so a no-op never even reindents.
		return data, report, nil
	}
	return out, report, nil
}

// dropRemoved deletes from mapping m every key that matches a removed path
// (concrete or list-wildcard), recursing into mappings and list entries. It
// returns the dotted paths it dropped.
func dropRemoved(m *yaml.Node, prefix string, idx map[string]Removed) []string {
	var dropped []string
	kept := m.Content[:0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		path := prefix + k.Value
		if _, ok := idx[path]; ok {
			dropped = append(dropped, path)
			continue
		}
		switch v.Kind {
		case yaml.MappingNode:
			dropped = append(dropped, dropRemoved(v, path+".", idx)...)
		case yaml.SequenceNode:
			for _, e := range v.Content {
				if e.Kind == yaml.MappingNode {
					dropped = append(dropped, dropRemovedEntry(e, path, idx)...)
				}
			}
		}
		kept = append(kept, k, v)
	}
	m.Content = kept
	return dropped
}

// dropRemovedEntry deletes the removed keys of one list entry, matching the
// "<list>[].<key>" wildcard.
func dropRemovedEntry(e *yaml.Node, listPath string, idx map[string]Removed) []string {
	var dropped []string
	kept := e.Content[:0]
	for i := 0; i+1 < len(e.Content); i += 2 {
		k, v := e.Content[i], e.Content[i+1]
		wildcard := listPath + "[]." + k.Value
		if _, ok := idx[wildcard]; ok {
			dropped = append(dropped, wildcard)
			continue
		}
		kept = append(kept, k, v)
	}
	e.Content = kept
	return dropped
}

// resetOff replaces a kept key's off value with the schema default. It walks
// the same way as dropRemoved but only at the top level and nested mappings:
// off resets apply to scalar keys.
func resetOff(m *yaml.Node, prefix string, offs map[string]string, s Schema) []string {
	var reset []string
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		path := prefix + k.Value
		if off, ok := offs[path]; ok && v.Kind == yaml.ScalarNode && v.Value == off {
			if key, found := s.Lookup(path); found {
				def := strings.Trim(key.Default, `"`)
				v.Value = def
				v.Tag = ""
				v.Style = 0
				reset = append(reset, path)
			}
			continue
		}
		if v.Kind == yaml.MappingNode {
			reset = append(reset, resetOff(v, path+".", offs, s)...)
		}
	}
	return reset
}

// walkMapping calls fn for every scalar/leaf key path in a mapping tree, with
// the list-entry keys flattened to their "<list>[].<key>" form.
func walkMapping(m *yaml.Node, prefix string, fn func(path string, v *yaml.Node)) {
	if m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		path := prefix + k.Value
		fn(path, v)
		switch v.Kind {
		case yaml.MappingNode:
			walkMapping(v, path+".", fn)
		case yaml.SequenceNode:
			for _, e := range v.Content {
				if e.Kind == yaml.MappingNode {
					for j := 0; j+1 < len(e.Content); j += 2 {
						ek := e.Content[j]
						fn(path+"[]."+ek.Value, e.Content[j+1])
					}
				}
			}
		}
	}
}

func dedupSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := in[:0]
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// equalYAML reports whether two YAML byte slices are the same document, so a
// realignment that only reindents is still reported as a reformat but one that
// changes nothing at all is a true no-op. It compares the canonical bytes.
func equalYAML(a, b []byte) bool {
	return string(a) == string(b)
}

// BackupName is the name the realignment gives the pre-upgrade copy of a file:
// "<file>.bak-<version>", kept beside the original (#903 WP1).
func BackupName(file, version string) string {
	return fmt.Sprintf("%s.bak-%s", file, version)
}
