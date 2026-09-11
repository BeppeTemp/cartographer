package clientconfig

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// workspace.go (D193) — the workspace↔KB↔provider binding.
//
// D169's per-provider binding is static: it does not depend on the directory a
// session is running in. That closed the originating incident for a provider
// dedicated to one perimeter, and could not close it for a provider used in
// two: one global catalogue, two perimeters, and a skill belonging to one of
// them offered in the other.
//
// A workspace binding is a *projection*, not an authorization boundary (D169):
// a process running as the same user can read any file on the machine. What it
// prevents is accidental exposure and activation. Do not promise more.

// ProjectionScope selects where a provider's KB artifacts are materialized.
const (
	// ScopeProvider is the historical behaviour: one global catalogue under the
	// user's home, shared by every session of that provider.
	ScopeProvider = "provider"
	// ScopeWorkspace materializes each workspace's KBs into that workspace's
	// own project-local directories, and materializes nothing KB-sourced
	// globally.
	ScopeWorkspace = "workspace"
)

// WorkspaceBinding binds one directory to the KBs a provider may receive while
// working in it.
//
// Path is canonical and absolute: it is resolved once, at bind time, and
// persisted. Remote is the normalized git remote of the repository at that path
// when there is one, recorded as a **guard** against moves and accidental
// reuse — never as a selector, because one remote has many clones and
// worktrees and choosing between them by remote would pick the wrong one.
//
// KBs is explicit and may be empty: "this workspace receives no KB artifacts"
// is a declaration, distinct from "this workspace is not bound", which is the
// absence of the entry. Neither ever means "every KB" — that is what
// fail-closed means here (decision 8).
type WorkspaceBinding struct {
	Path   string   `yaml:"path"`
	Remote string   `yaml:"remote,omitempty"`
	KBs    []string `yaml:"kbs"`
}

// gitRemoteFn is indirected for tests: a bind must work in a temp dir with no
// git at all, and a test must be able to fake a remote without a real clone.
var gitRemoteFn = gitRemoteOrigin

// gitRemoteOrigin returns the normalized `origin` URL of the repository
// containing dir, or "" when dir is not in a git repository (which is a legal
// workspace: a plain directory binds fine, it simply carries no guard).
func gitRemoteOrigin(dir string) string {
	cmd := exec.Command("git", "-C", dir, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return NormalizeRemote(string(out))
}

// NormalizeRemote reduces a git remote URL to a comparable form: trimmed,
// lowercased, without a trailing ".git" or "/", and with the scp-like SSH
// syntax rewritten to a path. It exists so `git@host:owner/repo.git` and
// `https://host/owner/repo` compare equal — the same repository reached two
// ways must not read as a moved workspace.
func NormalizeRemote(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		if at := strings.Index(s, "@"); at >= 0 && at < strings.Index(s+"/", "/") {
			s = s[at+1:]
		}
	} else if at := strings.Index(s, "@"); at >= 0 {
		// scp-like: user@host:owner/repo.git
		s = strings.Replace(s[at+1:], ":", "/", 1)
	}
	// Trailing "/" first, then ".git": a URL copied from a browser can carry
	// both ("…/repo.git/"), and trimming in the other order leaves the ".git".
	s = strings.TrimRight(s, "/")
	s = strings.TrimSuffix(s, ".git")
	return strings.TrimRight(s, "/")
}

// CanonicalWorkspacePath resolves dir to the absolute, symlink-free path that
// is persisted and compared. Resolving once at bind time is what makes the
// later comparison a string comparison rather than a filesystem walk.
func CanonicalWorkspacePath(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("clientconfig: empty workspace path")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("clientconfig: resolve workspace path %q: %w", dir, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// The path must exist to be bound: binding a directory that is not
		// there records a guard nothing can ever check.
		return "", fmt.Errorf("clientconfig: workspace path %q: %w", dir, err)
	}
	return resolved, nil
}

// WorkspaceScope reports the projection scope configured for provider.
// The zero value is ScopeProvider, so every existing configuration keeps the
// global catalogue it has: a new scope must never switch under anyone
// (decision: migration is explicit).
func (c *Config) WorkspaceScope(provider string) string {
	if c.Scopes == nil {
		return ScopeProvider
	}
	if s, ok := c.Scopes[provider]; ok && s == ScopeWorkspace {
		return ScopeWorkspace
	}
	return ScopeProvider
}

// SetWorkspaceScope records provider's projection scope. Setting it to
// ScopeProvider removes the entry, so the file does not accumulate keys that
// restate the default.
func (c *Config) SetWorkspaceScope(provider, scope string) error {
	switch scope {
	case ScopeProvider:
		delete(c.Scopes, provider)
		return nil
	case ScopeWorkspace:
		if c.Scopes == nil {
			c.Scopes = map[string]string{}
		}
		c.Scopes[provider] = ScopeWorkspace
		return nil
	default:
		return fmt.Errorf("clientconfig: unknown projection scope %q (want %q or %q)", scope, ScopeProvider, ScopeWorkspace)
	}
}

// WorkspaceBindings returns provider's workspace bindings, as a copy.
func (c *Config) WorkspaceBindings(provider string) []WorkspaceBinding {
	out := make([]WorkspaceBinding, 0, len(c.Workspaces[provider]))
	for _, w := range c.Workspaces[provider] {
		w.KBs = append([]string(nil), w.KBs...)
		out = append(out, w)
	}
	return out
}

// BindWorkspace binds dir to kbs for provider, replacing any existing binding
// for the same canonical path. kbs may be empty — an explicit "no KB artifacts
// here" — but never nil-means-all.
//
// The git remote is recorded when dir is in a repository. It is a guard: a
// later sync that finds a different remote at the same path refuses rather than
// projecting one perimeter's artifacts into another's checkout.
func (c *Config) BindWorkspace(provider, dir string, kbs []string) error {
	if provider == "" || provider != strings.TrimSpace(provider) {
		return fmt.Errorf("clientconfig: invalid provider name %q", provider)
	}
	path, err := CanonicalWorkspacePath(dir)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("clientconfig: workspace %q is not a directory", dir)
	}
	for _, kb := range kbs {
		if kb == "" || kb != strings.TrimSpace(kb) {
			return fmt.Errorf("clientconfig: invalid KB name %q", kb)
		}
	}
	if c.Workspaces == nil {
		c.Workspaces = map[string][]WorkspaceBinding{}
	}
	binding := WorkspaceBinding{
		Path:   path,
		Remote: gitRemoteFn(path),
		KBs:    append([]string{}, kbs...),
	}
	list := c.Workspaces[provider]
	for i := range list {
		if list[i].Path == path {
			list[i] = binding
			c.Workspaces[provider] = list
			return nil
		}
	}
	c.Workspaces[provider] = append(list, binding)
	return nil
}

// UnbindWorkspace removes provider's binding for dir. Unbinding a path that is
// not bound is a silent no-op, matching Unbind. The path is canonicalized when
// it still exists and compared verbatim when it does not, so a workspace whose
// directory was deleted can still be removed from the config.
func (c *Config) UnbindWorkspace(provider, dir string) error {
	if provider == "" {
		return fmt.Errorf("clientconfig: invalid provider name %q", provider)
	}
	path, err := CanonicalWorkspacePath(dir)
	if err != nil {
		abs, absErr := filepath.Abs(dir)
		if absErr != nil {
			return err
		}
		path = abs
	}
	list := c.Workspaces[provider]
	kept := make([]WorkspaceBinding, 0, len(list))
	for _, w := range list {
		if w.Path != path {
			kept = append(kept, w)
		}
	}
	if len(kept) == 0 {
		delete(c.Workspaces, provider)
		return nil
	}
	c.Workspaces[provider] = kept
	return nil
}

// WorkspaceLookupError distinguishes the ways a bound workspace can fail to
// resolve. Each is an error, never a fallback to "every KB": falling back is
// how a perimeter's artifacts end up in another's checkout.
type WorkspaceLookupError struct {
	Path   string
	Reason string
	Detail string
}

func (e *WorkspaceLookupError) Error() string {
	return fmt.Sprintf("workspace %s: %s (%s)", e.Path, e.Reason, e.Detail)
}

// ResolveWorkspace returns provider's binding for the workspace containing dir.
//
// The match is the **longest** bound path that is dir or a parent of it, so a
// repository bound inside another bound directory wins over the outer one. A
// dir under no bound path returns ok=false with no error: that is an unbound
// workspace, which is fail-closed for KB artifacts and receives only the
// transversal bundle — a legal state, not a failure.
//
// A bound path that no longer exists, or whose git remote no longer matches the
// one recorded at bind time, returns an error. Both mean the binding describes
// something that is not there any more, and continuing would project a
// perimeter's artifacts into a checkout nobody bound.
func (c *Config) ResolveWorkspace(provider, dir string) (WorkspaceBinding, bool, error) {
	list := c.Workspaces[provider]
	if len(list) == 0 {
		return WorkspaceBinding{}, false, nil
	}
	target, err := CanonicalWorkspacePath(dir)
	if err != nil {
		return WorkspaceBinding{}, false, err
	}

	best := -1
	for i, w := range list {
		if !pathWithin(target, w.Path) {
			continue
		}
		if best < 0 || len(w.Path) > len(list[best].Path) {
			best = i
		}
	}
	if best < 0 {
		return WorkspaceBinding{}, false, nil
	}
	w := list[best]

	if info, err := os.Stat(w.Path); err != nil || !info.IsDir() {
		return WorkspaceBinding{}, false, &WorkspaceLookupError{
			Path: w.Path, Reason: "bound directory is gone",
			Detail: "rebind it with `cartographer workspace bind`, or remove the binding with `workspace unbind`",
		}
	}
	if w.Remote != "" {
		if got := gitRemoteFn(w.Path); got != w.Remote {
			return WorkspaceBinding{}, false, &WorkspaceLookupError{
				Path: w.Path, Reason: "git remote changed since it was bound",
				Detail: fmt.Sprintf("bound to %s, found %q — rebind deliberately rather than projecting another perimeter's artifacts here", w.Remote, got),
			}
		}
	}
	w.KBs = append([]string(nil), w.KBs...)
	return w, true, nil
}

// pathWithin reports whether target is root or a descendant of it. It compares
// canonical paths textually, which is what makes the check cheap enough to run
// on every sync.
func pathWithin(target, root string) bool {
	if target == root {
		return true
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
