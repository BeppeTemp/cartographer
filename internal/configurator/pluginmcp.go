package configurator

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// The MCP side of a plugin provider that keeps its own MCP file: a `*` in the
// path expands to every installed version of the client (applyGlob), and the
// files the client derives from it and regenerates (PluginSpec.MCPStale) are
// removed after a write that changed bytes (removeStale).

// maxVersionMatches is how many stale files one write may remove: a wider
// pattern is refused, because it is the only thing standing between a descriptor
// typo and a deleted directory's worth of files.
const maxVersionMatches = 3

// maxMCPFile caps what is read of a client's file when only its bytes are
// compared: it is the client's file, not ours, and a huge one is skipped.
const maxMCPFile = 4 << 20

// UsesNoMCP reports whether the provider gets neither an MCP entry nor the
// command route (MCPModeNone); false for an unknown provider. Connect, sync,
// disconnect and doctor skip it without a warning.
func UsesNoMCP(p Provider) bool {
	d, ok := Lookup(p)
	return ok && d.MCPMode == MCPModeNone
}

// expandVersionGlob resolves home-relative segments, one of which may hold a glob
// character, to the existing regular files they name. The glob segment is a
// ReadDir of its fixed parent matched with filepath.Match, so a home holding
// '[' or '*' is never read as a pattern, and a symlinked entry is not a match.
// A match behind a link or another non-plain component is refused with a
// warning (safewrite.CheckPath), never followed, and only a regular file counts.
func expandVersionGlob(home string, segs []string) (paths, warnings []string) {
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

// globMatches expands the MCP pattern of a registered plugin provider into
// home-relative paths (OS separators).
func globMatches(d Descriptor, home string) (rels []string, warnings []string) {
	abs, warns := expandVersionGlob(home, strings.Split(d.MCPConfigGlob, "/"))
	for _, p := range abs {
		if rel, err := filepath.Rel(home, p); err == nil {
			rels = append(rels, rel)
		}
	}
	return rels, warns
}

// applyGlob writes r, whose FileGlob names a pattern, into every file the pattern
// matches under baseDir (a client keeps one file per installed version), through
// Apply, so each write is the guarded, non-destructive, atomic one. No match is a
// warning, not an error: the client has not been opened yet.
func applyGlob(r *EmitResult, baseDir string, dryRun bool) (written []string, refused []error, err error) {
	d, ok := Lookup(r.Provider)
	if !ok || d.MCPConfigGlob == "" {
		return nil, nil, fmt.Errorf("%s has no MCP file pattern", r.Provider)
	}
	rels, warns := globMatches(d, baseDir)
	r.Warnings = append(r.Warnings, warns...)
	if len(rels) == 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("no MCP config of %s found: open the client once", r.Provider))
		return nil, nil, nil
	}
	for _, rel := range rels {
		cp := *r
		cp.FileGlob, cp.FilePath, cp.Warnings, cp.viaGlob = "", rel, nil, true
		w, werr := Apply([]*EmitResult{&cp}, baseDir, dryRun)
		written = append(written, w...)
		r.Warnings = append(r.Warnings, cp.Warnings...)
		if werr != nil {
			if errors.Is(werr, safewrite.ErrSymlinkDestination) {
				refused = append(refused, werr)
				continue
			}
			return written, refused, werr
		}
	}
	if len(written) > 0 && !dryRun {
		r.Warnings = append(r.Warnings, removeStale(r.Provider, baseDir)...)
	}
	return written, refused, nil
}

// removeStale removes, after an entry write that changed bytes, the files the
// client derives from the file Cartographer wrote and regenerates. Only the
// files the provider's stale templates match are touched, each one passing
// safewrite.CheckPath; more than maxVersionMatches matches, or a match behind a
// link, removes none. It returns the notes to show.
func removeStale(p Provider, home string) []string {
	spec, ok := PluginOf(p)
	if !ok || len(spec.MCPStale) == 0 {
		return nil
	}
	var matches, notes []string
	for _, tpl := range spec.MCPStale {
		paths, warns := expandVersionGlob(home, tpl)
		notes = append(notes, warns...)
		matches = append(matches, paths...)
	}
	if len(notes) > 0 {
		return append(notes, fmt.Sprintf("%s: a stale file is behind a link: none removed", p))
	}
	if len(matches) > maxVersionMatches {
		return []string{fmt.Sprintf("%s: mcp.stale matches %d files (at most %d are removed): none removed", p, len(matches), maxVersionMatches)}
	}
	var removed []string
	for _, path := range matches {
		if err := safewrite.Remove(home, path); err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		removed = append(removed, path)
	}
	if len(removed) > 0 {
		notes = append(notes, fmt.Sprintf("%s: %d derived file(s) reset (%s): servers added through the client's UI are dropped when it next starts; restart the window", p, len(removed), strings.Join(removed, ", ")))
	}
	return notes
}

// readBounded reads a client's file for a byte comparison; "" for a file that is
// absent, not regular or over the cap.
func readBounded(path string) []byte {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxMCPFile {
		return nil
	}
	b, _ := os.ReadFile(path)
	return b
}

// removeGlob is removeEntry for a provider whose MCP file is a pattern: the entry
// is taken out of every match. Stale files are not touched again.
func removeGlob(cfg *ServerConfig, provider Provider, d Descriptor, baseDir string, dryRun bool) (bool, error) {
	rels, _ := globMatches(d, baseDir)
	removed := false
	for _, rel := range rels {
		ok, err := removeEntryAt(cfg, provider, baseDir, rel, dryRun)
		if err != nil {
			return removed, err
		}
		removed = removed || ok
	}
	return removed, nil
}

// MCPFiles lists the absolute paths of the MCP file of d under baseDir: the
// literal one (whether or not it exists), or, for a provider whose file is a
// pattern, the existing regular files it matches, each passing safewrite.CheckPath
// (so a caller that reads or writes them follows no link). Empty for a provider
// with no MCP file (cli, none). A caller that needs the file of a provider uses
// this, never MCPConfigPath alone: that field is empty for a pattern.
func (d Descriptor) MCPFiles(baseDir string) []string {
	switch {
	case d.MCPConfigGlob != "":
		abs, _ := expandVersionGlob(baseDir, strings.Split(d.MCPConfigGlob, "/"))
		return abs
	case d.MCPConfigPath != "":
		return []string{filepath.Join(baseDir, d.ConfigPath())}
	}
	return nil
}
