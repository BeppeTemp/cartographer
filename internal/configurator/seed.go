package configurator

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/jsonorder"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// MCPModeSeed is the VariantSpec.MCPMode of a variant whose client re-seeds its
// MCP settings from a plaintext template shipped with an extension (D1700). The
// variant has no MCP file of its own: Cartographer's entry is merged into the
// template and the client's derived store is reset when the template changed.
const MCPModeSeed = "seed"

// maxSeedDerived is how many derived files one run may remove: a wider glob is
// refused, because it is the only thing standing between a descriptor typo and
// a deleted directory's worth of files.
const maxSeedDerived = 3

// SeedOf returns the spec of a registered variant in seed mode.
func SeedOf(p Provider) (VariantSpec, bool) {
	s, ok := VariantOf(p)
	return s, ok && s.MCPMode == MCPModeSeed
}

// SeedResult is what a seed or unseed run did (or, under dryRun, would do).
type SeedResult struct {
	Templates []string // templates rewritten
	Removed   []string // derived files removed
	Warnings  []string
}

// SeedMCP merges Cartographer's server entry name/spec into every template
// matching the seed variant's template glob under home, using the BASE
// provider's emitter. Other servers in the template stay. Only when a template
// was rewritten in this run are the derived files removed (a template the
// previous run already seeded changes nothing, so a user's own servers in the
// derived store survive a no-op sync). A template that is not a JSON object is
// skipped with a warning and never touched. Nothing else is written or deleted.
func SeedMCP(p Provider, name string, spec ServerSpec, home string, dryRun bool) (SeedResult, error) {
	var res SeedResult
	vs, ok := SeedOf(p)
	if !ok {
		return res, fmt.Errorf("%s is not a seed variant", p)
	}
	if err := validateServerSpec(name, spec); err != nil {
		return res, err
	}
	base, _ := builtin(Provider(vs.Base))
	r, err := base.emit(name, spec)
	if err != nil {
		return res, err
	}
	var emitted map[string]any
	if err := json.Unmarshal(r.Content, &emitted); err != nil {
		return res, fmt.Errorf("seed %s: base %s emitted no JSON object: %w", p, vs.Base, err)
	}
	// Only the server entry travels: a base's other top-level keys ($schema...)
	// are its own file's business, not the template's.
	incoming, key, entry := map[string]any{}, "", any(nil)
	for _, k := range mcpServerKeys {
		if servers, ok := emitted[k].(map[string]any); ok && servers[name] != nil {
			key, entry = k, servers[name]
			incoming[k] = map[string]any{name: entry}
			break
		}
	}
	if key == "" {
		return res, fmt.Errorf("seed %s: base %s emitted no entry %q", p, vs.Base, name)
	}
	incomingJSON, _ := json.Marshal(incoming)

	templates, warns := expandSeedGlob(home, vs.SeedTemplate)
	res.Warnings = append(res.Warnings, warns...)
	for _, path := range templates {
		data, err := readCapped(path)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %s: cannot read the seed template (%v); left alone", p, path, err))
			continue
		}
		var existing map[string]any
		if json.Unmarshal(data, &existing) != nil || existing == nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %s is not a JSON object; left untouched, Cartographer is not seeded there", p, path))
			continue
		}
		if servers, ok := existing[key].(map[string]any); ok && reflect.DeepEqual(servers[name], entry) {
			continue
		}
		merged, err := mergeJSON(data, incomingJSON)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %s: %v; left untouched", p, path, err))
			continue
		}
		res.Templates = append(res.Templates, path)
		if dryRun {
			continue
		}
		perm := os.FileMode(0o644)
		if info, err := os.Lstat(path); err == nil {
			perm = info.Mode().Perm()
		}
		if err := safewrite.WriteFileAtomicNoFollow(home, path, merged, perm); err != nil {
			return res, fmt.Errorf("seed %s: %w", path, err)
		}
	}
	if len(res.Templates) > 0 {
		res.removeDerived(p, vs, home, dryRun)
	}
	return res, nil
}

// UnseedMCP removes the entries named names from every matching template and
// touches nothing else; the derived store is NOT reset (the least destructive
// choice: the extension's own UI removes the server there).
func UnseedMCP(p Provider, names []string, home string, dryRun bool) (SeedResult, error) {
	var res SeedResult
	vs, ok := SeedOf(p)
	if !ok {
		return res, fmt.Errorf("%s is not a seed variant", p)
	}
	templates, warns := expandSeedGlob(home, vs.SeedTemplate)
	res.Warnings = append(res.Warnings, warns...)
	for _, path := range templates {
		data, err := readCapped(path)
		if err != nil {
			continue
		}
		root, layout, err := jsonorder.Parse(data)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %s is not valid JSON; left untouched", p, path))
			continue
		}
		found := false
		for _, key := range mcpServerKeys {
			servers, ok := root[key].(map[string]any)
			for _, n := range names {
				if _, has := servers[n]; ok && has {
					found = true
					delete(servers, n)
				}
			}
			if ok && len(servers) == 0 {
				delete(root, key)
			}
		}
		if !found {
			continue
		}
		res.Templates = append(res.Templates, path)
		if dryRun {
			continue
		}
		out, err := jsonorder.Marshal(root, layout)
		if err != nil {
			return res, err
		}
		perm := os.FileMode(0o644)
		if info, err := os.Lstat(path); err == nil {
			perm = info.Mode().Perm()
		}
		if err := safewrite.WriteFileAtomicNoFollow(home, path, out, perm); err != nil {
			return res, fmt.Errorf("unseed %s: %w", path, err)
		}
	}
	return res, nil
}

// removeDerived deletes the derived files after a template changed. More than
// maxSeedDerived matches, or a match behind a link, deletes nothing.
func (res *SeedResult) removeDerived(p Provider, vs VariantSpec, home string, dryRun bool) {
	derived, warns := expandSeedGlob(home, vs.SeedDerived)
	res.Warnings = append(res.Warnings, warns...)
	if len(derived) > maxSeedDerived {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s: seed.derived_glob matches %d files (at most %d are removed): none removed", p, len(derived), maxSeedDerived))
		return
	}
	for _, path := range derived {
		if !dryRun {
			if err := safewrite.Remove(home, path); err != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %v", p, err))
				continue
			}
		}
		res.Removed = append(res.Removed, path)
	}
	verb := "reset"
	if dryRun {
		verb = "would be reset"
	}
	if len(res.Removed) > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %s %s (%s): servers added through the extension's UI are dropped when it next starts; restart the window", p, plural(len(res.Removed), "derived file"), verb, strings.Join(res.Removed, ", ")))
	}
}

// maxSeedTemplate caps what is read of a template: it is the extension's file,
// not ours, and a huge one is skipped rather than loaded.
const maxSeedTemplate = 4 << 20

func readCapped(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSeedTemplate {
		return nil, fmt.Errorf("not a regular file of at most %d bytes", maxSeedTemplate)
	}
	return os.ReadFile(path)
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// expandSeedGlob resolves home-relative segments, one of which may hold a glob
// character, to the existing regular files they name. The glob segment is a
// ReadDir of its fixed parent matched with filepath.Match, so a home holding
// '[' or '*' is never read as a pattern, and a symlinked entry is not a match.
// A match behind a link or another non-plain component is refused with a
// warning, never followed.
func expandSeedGlob(home string, segs []string) (paths, warnings []string) {
	if home == "" || len(segs) == 0 {
		return nil, nil
	}
	cur := []string{home}
	for _, seg := range segs {
		var next []string
		for _, dir := range cur {
			if !strings.ContainsAny(seg, "*?[") {
				next = append(next, filepath.Join(dir, seg))
				continue
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.Type()&fs.ModeSymlink != 0 {
					continue
				}
				if ok, _ := filepath.Match(seg, e.Name()); ok {
					next = append(next, filepath.Join(dir, e.Name()))
				}
			}
		}
		cur = next
	}
	for _, path := range cur {
		if _, err := os.Lstat(path); err != nil {
			continue
		}
		if err := safewrite.CheckPath(home, path); err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
			paths = append(paths, path)
		}
	}
	return paths, warnings
}
