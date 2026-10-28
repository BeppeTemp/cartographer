package clientconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/repoindex"
)

// derived.go (D1019) — the workspace bindings a KB implies.
//
// A repository a KB declares in its paths.yaml `repos:` is bound to that KB for
// every provider in workspace scope. That binding is *derived state*, not a
// preference: it is recomputed by every `sync` from what the KBs declare and
// from where the clones are on this machine, so it lives apart from
// `.cartographer.yaml` (which is the user's file, D169), next to the repo index
// cache it depends on. The explicit `workspaces:` table stays the only thing
// the user writes.

// DerivedFileName is the derived bindings' file, in the directory of the repo
// index cache.
const DerivedFileName = "workspace-derived.json"

const derivedVersion = 1

// DerivedSource says why a derived binding exists: the KB that declares the
// repository and the key it declares it under.
type DerivedSource struct {
	KB  string `json:"kb"`
	Key string `json:"key"`
}

// DerivedBinding is one repository bound to the KBs that declare it.
type DerivedBinding struct {
	// Path is canonical (CanonicalWorkspacePath); Remote is the guard, as for
	// an explicit binding.
	Path    string          `json:"path"`
	Remote  string          `json:"remote,omitempty"`
	KBs     []string        `json:"kbs"`
	Sources []DerivedSource `json:"sources,omitempty"`
}

// DerivedUnresolved is a declared repository that resolved to no usable local
// clone, kept so `status` can say so.
type DerivedUnresolved struct {
	KB     string `json:"kb"`
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

// DerivedState is the content of the derived bindings file. ConfigDir names the
// client config directory it was derived for: a file written for another
// directory (another profile, a test) is not this configuration's.
type DerivedState struct {
	Version    int                            `json:"version"`
	ConfigDir  string                         `json:"config_dir"`
	Bindings   map[string][]DerivedBinding    `json:"bindings,omitempty"`
	Unresolved map[string][]DerivedUnresolved `json:"unresolved,omitempty"`
}

// DerivedPath is where the derived bindings are stored.
func DerivedPath() (string, error) {
	cache, err := repoindex.CachePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(cache), DerivedFileName), nil
}

// LoadDerived reads the derived bindings recorded for the config directory dir.
// A missing, unreadable, foreign or newer-format file is an empty state, never
// an error: derived state is rebuilt by the next sync, and a reader must not
// fail because of it.
func LoadDerived(dir string) DerivedState {
	path, err := DerivedPath()
	if err != nil {
		return DerivedState{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return DerivedState{}
	}
	var s DerivedState
	if json.Unmarshal(data, &s) != nil || s.Version != derivedVersion || s.ConfigDir != dir {
		return DerivedState{}
	}
	return s
}

// derivedBytes is the file SaveDerived writes for s.
func derivedBytes(dir string, s DerivedState) (string, []byte, error) {
	path, err := DerivedPath()
	if err != nil {
		return "", nil, err
	}
	s.Version, s.ConfigDir = derivedVersion, dir
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", nil, fmt.Errorf("clientconfig: marshal derived bindings: %w", err)
	}
	return path, data, nil
}

// DerivedWouldChange reports whether SaveDerived would create or change the
// file, and its path, without writing anything (#1268).
func DerivedWouldChange(dir string, s DerivedState) (path string, changed bool, err error) {
	path, data, err := derivedBytes(dir, s)
	if err != nil {
		return "", false, err
	}
	old, rerr := os.ReadFile(path)
	return path, rerr != nil || string(old) != string(data), nil
}

// SaveDerived records s as the derived bindings of the config directory dir.
func SaveDerived(dir string, s DerivedState) error {
	path, data, err := derivedBytes(dir, s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("clientconfig: mkdir %s: %w", filepath.Dir(path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), DerivedFileName+".tmp*")
	if err != nil {
		return fmt.Errorf("clientconfig: write %s: %w", path, err)
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("clientconfig: write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("clientconfig: write %s: %w", path, err)
	}
	return nil
}

// SetDerived installs s as the derived bindings this configuration reads. It is
// in memory only: SaveDerived persists.
func (c *Config) SetDerived(s DerivedState) { c.derived = s }

// Derived returns the derived bindings this configuration reads.
func (c *Config) Derived() DerivedState { return c.derived }

// RenameDerivedKB renames a KB in the derived bindings. With newName empty the
// KB is removed instead: it leaves every binding's KBs and sources, a binding
// left with no KB is dropped (an empty derived binding would read as an opt-out),
// and its unresolved entries go. It reports whether anything changed; the caller
// persists with SaveDerived.
func (c *Config) RenameDerivedKB(old, newName string) bool {
	changed := false
	rename := func(kbs []string) []string {
		out := make([]string, 0, len(kbs))
		for _, k := range kbs {
			if k == old {
				changed = true
				if newName == "" || containsString(kbs, newName) || containsString(out, newName) {
					continue
				}
				k = newName
			}
			out = append(out, k)
		}
		return out
	}
	for p, list := range c.derived.Bindings {
		kept := list[:0:0]
		for _, b := range list {
			b.KBs = rename(b.KBs)
			var src []DerivedSource
			for _, s := range b.Sources {
				if s.KB == old {
					changed = true
					if newName == "" {
						continue
					}
					s.KB = newName
				}
				src = append(src, s)
			}
			b.Sources = src
			if len(b.KBs) > 0 {
				kept = append(kept, b)
			}
		}
		if len(kept) == 0 {
			delete(c.derived.Bindings, p)
		} else {
			c.derived.Bindings[p] = kept
		}
	}
	for p, list := range c.derived.Unresolved {
		var kept []DerivedUnresolved
		for _, u := range list {
			if u.KB == old {
				changed = true
				if newName == "" {
					continue
				}
				u.KB = newName
			}
			kept = append(kept, u)
		}
		if len(kept) == 0 {
			delete(c.derived.Unresolved, p)
		} else {
			c.derived.Unresolved[p] = kept
		}
	}
	return changed
}

// DerivedUnresolved returns, for provider, the declared repositories that
// resolved to no usable clone. Empty unless provider is in workspace scope.
func (c *Config) DerivedUnresolved(provider string) []DerivedUnresolved {
	if c.WorkspaceScope(provider) != ScopeWorkspace {
		return nil
	}
	return append([]DerivedUnresolved(nil), c.derived.Unresolved[provider]...)
}

// effectiveWorkspaces is provider's explicit bindings plus the derived ones.
// An explicit binding replaces the derived one on the same canonical path
// entirely, `kbs: []` included: that is the local opt-out. Derived bindings
// exist only for a provider in workspace scope, so a provider in provider scope
// reads exactly what it always read.
func (c *Config) effectiveWorkspaces(provider string) []WorkspaceBinding {
	out := append([]WorkspaceBinding(nil), c.Workspaces[provider]...)
	if c.WorkspaceScope(provider) != ScopeWorkspace {
		return out
	}
	explicit := make(map[string]bool, len(out))
	for _, w := range out {
		explicit[w.Path] = true
	}
	for _, d := range c.derived.Bindings[provider] {
		if explicit[d.Path] {
			continue
		}
		w := WorkspaceBinding{Path: d.Path, Remote: d.Remote, KBs: d.KBs, Derived: true}
		for _, s := range d.Sources {
			w.Sources = append(w.Sources, s.KB)
		}
		out = append(out, w)
	}
	return out
}

// DeclaredRepo is one `repos:` entry of a KB that binds (bind is not false).
type DeclaredRepo struct {
	KB, Key, Remote string
}

// DeriveBindings turns the declared repositories into bindings. resolve maps a
// declaration to the local clone the placeholders resolve to; an error leaves
// it unbound and listed. Declarations that resolve to the same canonical path
// merge into one binding with the union of their KBs, in decls order.
//
// The binding is by path and the remote is the guard (D193): an entry without
// a remote, and a clone whose origin is empty or not the declared remote, are
// not bound. The recorded Remote is the
// one found at the path, in the form ResolveWorkspace compares.
func DeriveBindings(decls []DeclaredRepo, resolve func(DeclaredRepo) (string, error)) ([]DerivedBinding, []DerivedUnresolved) {
	var out []DerivedBinding
	var bad []DerivedUnresolved
	byPath := map[string]int{}
	for _, d := range decls {
		unresolved := func(reason string) {
			bad = append(bad, DerivedUnresolved{KB: d.KB, Key: d.Key, Reason: reason})
		}
		// paths.yaml is unsigned KB content: the remote is the only thing that
		// makes binding a clone safe, so an entry without one binds nothing.
		if d.Remote == "" {
			unresolved("declared without a remote: not bound (the remote is the guard)")
			continue
		}
		raw, err := resolve(d)
		if err != nil {
			unresolved("declared, no local clone")
			continue
		}
		path, err := CanonicalWorkspacePath(raw)
		if err != nil {
			unresolved("declared, no local clone")
			continue
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			unresolved("declared, no local clone")
			continue
		}
		actual := gitRemoteFn(path)
		if actual == "" || remoteHostPath(actual) != remoteHostPath(d.Remote) {
			unresolved(fmt.Sprintf("declared, but the clone at %s has no origin or another remote", path))
			continue
		}
		src := DerivedSource{KB: d.KB, Key: d.Key}
		if i, ok := byPath[path]; ok {
			b := &out[i]
			if !containsString(b.KBs, d.KB) {
				b.KBs = append(b.KBs, d.KB)
			}
			b.Sources = append(b.Sources, src)
			continue
		}
		byPath[path] = len(out)
		out = append(out, DerivedBinding{Path: path, Remote: actual, KBs: []string{d.KB}, Sources: []DerivedSource{src}})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, bad
}

// remoteHostPath reduces a normalized remote to host/owner/name without a port
// or case, the form a KB declares: repoindex drops the port, NormalizeRemote
// keeps it.
func remoteHostPath(r string) string {
	r = strings.ToLower(strings.TrimSpace(r))
	host, rest, ok := strings.Cut(r, "/")
	if !ok {
		return r
	}
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	return host + "/" + rest
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
