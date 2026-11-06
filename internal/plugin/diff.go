package plugin

import (
	"crypto/sha256"
	"fmt"
	"io"
	"maps"
	"slices"
)

// FileChange is one file that differs between two trees.
type FileChange struct {
	Path             string
	OldSize, NewSize int
	// Mode is true when only the executable bit changed.
	Mode bool
}

// Delta is what an update changes against the approved tree: the text the user
// reads before consenting. Everything in it is plugin-supplied, so DiffPrint
// escapes it.
type Delta struct {
	Added, Removed []string
	Changed        []FileChange
	// OldVersion and NewVersion are the manifest versions.
	OldVersion, NewVersion string
	// CartographerFrom and CartographerTo are the `cartographer` ranges, equal when unchanged.
	CartographerFrom, CartographerTo string
	// DependsAdded and DependsRemoved are "name range" lines; a changed range is one of each.
	DependsAdded, DependsRemoved []string
	// PermissionsAdded and PermissionsRemoved are one human line each.
	PermissionsAdded, PermissionsRemoved []string
	// PathsAdded and PathsRemoved are the provider destinations resolved on this machine.
	PathsAdded, PathsRemoved []string
}

// Empty reports whether nothing the user would read differs.
func (d Delta) Empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0 &&
		d.CartographerFrom == d.CartographerTo && len(d.DependsAdded) == 0 && len(d.DependsRemoved) == 0 &&
		len(d.PermissionsAdded) == 0 && len(d.PermissionsRemoved) == 0 && len(d.PathsAdded) == 0 && len(d.PathsRemoved) == 0
}

// Diff compares an approved tree with a newer one, resolving provider
// destinations against env (the machine's home).
func Diff(old, next Plugin, env ProviderEnv) Delta {
	d := Delta{OldVersion: old.Manifest.Version, NewVersion: next.Manifest.Version,
		CartographerFrom: old.Manifest.Cartographer, CartographerTo: next.Manifest.Cartographer}
	oldFiles := map[string]TreeFile{}
	for _, f := range old.Files {
		oldFiles[f.Path] = f
	}
	seen := map[string]bool{}
	for _, f := range next.Files {
		seen[f.Path] = true
		o, ok := oldFiles[f.Path]
		switch {
		case !ok:
			d.Added = append(d.Added, fmt.Sprintf("%s (%d bytes)", f.Path, len(f.Content)))
		case sha256.Sum256(o.Content) != sha256.Sum256(f.Content) || o.Executable != f.Executable:
			d.Changed = append(d.Changed, FileChange{Path: f.Path, OldSize: len(o.Content), NewSize: len(f.Content),
				Mode: o.Executable != f.Executable && sha256.Sum256(o.Content) == sha256.Sum256(f.Content)})
		}
	}
	for _, f := range old.Files {
		if !seen[f.Path] {
			d.Removed = append(d.Removed, fmt.Sprintf("%s (%d bytes)", f.Path, len(f.Content)))
		}
	}
	d.DependsAdded, d.DependsRemoved = setDelta(dependLines(old.Manifest.Depends), dependLines(next.Manifest.Depends))
	d.PermissionsAdded, d.PermissionsRemoved = setDelta(permissionLines(old.Manifest.Permissions), permissionLines(next.Manifest.Permissions))
	d.PathsAdded, d.PathsRemoved = setDelta(providerPaths(old, env), providerPaths(next, env))
	return d
}

func dependLines(m map[string]string) []string {
	var out []string
	for _, n := range slices.Sorted(maps.Keys(m)) {
		out = append(out, n+" "+m[n])
	}
	return out
}

// permissionLines flattens a permissions block into comparable lines.
func permissionLines(p *Permissions) []string {
	if p == nil {
		return nil
	}
	var out []string
	if p.KB != "" {
		out = append(out, "kb: "+p.KB)
	}
	for _, v := range p.Net {
		out = append(out, "net: "+v)
	}
	for _, v := range p.Env {
		out = append(out, "env: "+v)
	}
	if p.Files != nil {
		for _, v := range p.Files.Read {
			out = append(out, "files read: "+v)
		}
		for _, v := range p.Files.Write {
			out = append(out, "files write: "+v)
		}
	}
	return out
}

// providerPaths is every destination line of every provider of p on env's machine.
func providerPaths(p Plugin, env ProviderEnv) []string {
	var out []string
	for _, r := range p.Providers(env) {
		for _, l := range r.Paths(env.Home) {
			out = append(out, r.Name+" "+l)
		}
	}
	return out
}

// setDelta returns what next has that old lacks and the reverse, sorted.
func setDelta(old, next []string) (added, removed []string) {
	in := func(list []string) map[string]bool {
		m := map[string]bool{}
		for _, s := range list {
			m[s] = true
		}
		return m
	}
	o, n := in(old), in(next)
	for s := range n {
		if !o[s] {
			added = append(added, s)
		}
	}
	for s := range o {
		if !n[s] {
			removed = append(removed, s)
		}
	}
	slices.Sort(added)
	slices.Sort(removed)
	return added, removed
}

// DiffPrint writes d in plain words. A permission added is flagged with "!!" and
// listed first: it is the line the consent turns on. Every plugin-supplied
// string goes through esc.
func DiffPrint(w io.Writer, name string, d Delta) {
	fmt.Fprintf(w, "changes in %s, approved %s -> new %s:\n", esc(name), esc(d.OldVersion), esc(d.NewVersion))
	if d.Empty() {
		fmt.Fprintln(w, "  no file, permission, range or destination differs")
		return
	}
	for _, p := range d.PermissionsAdded {
		fmt.Fprintf(w, "  !! PERMISSION ADDED: %s\n", esc(p))
	}
	for _, p := range d.PermissionsRemoved {
		fmt.Fprintf(w, "  permission removed: %s\n", esc(p))
	}
	if d.CartographerFrom != d.CartographerTo {
		fmt.Fprintf(w, "  needs Cartographer: %s -> %s\n", esc(d.CartographerFrom), esc(d.CartographerTo))
	}
	for _, s := range d.DependsAdded {
		fmt.Fprintf(w, "  depends added: %s\n", esc(s))
	}
	for _, s := range d.DependsRemoved {
		fmt.Fprintf(w, "  depends removed: %s\n", esc(s))
	}
	for _, s := range d.PathsAdded {
		fmt.Fprintf(w, "  writes on this machine, added: %s\n", esc(s))
	}
	for _, s := range d.PathsRemoved {
		fmt.Fprintf(w, "  writes on this machine, removed: %s\n", esc(s))
	}
	for _, s := range d.Added {
		fmt.Fprintf(w, "  file added: %s\n", esc(s))
	}
	for _, s := range d.Removed {
		fmt.Fprintf(w, "  file removed: %s\n", esc(s))
	}
	for _, c := range d.Changed {
		if c.Mode {
			fmt.Fprintf(w, "  file executable bit changed: %s\n", esc(c.Path))
			continue
		}
		fmt.Fprintf(w, "  file changed: %s (%d -> %d bytes)\n", esc(c.Path), c.OldSize, c.NewSize)
	}
}
