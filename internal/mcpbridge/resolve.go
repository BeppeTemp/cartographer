// Package mcpbridge is the local stdio MCP server that scopes one agent session
// to the KBs of the folder it was started in (D737).
//
// A provider with a workspace binding gets this bridge as its global MCP entry
// instead of the HTTP URL. The bridge resolves its working directory against the
// client's binding table, then proxies every JSON-RPC message of the session to
// the routed endpoint with ?kbs=<set>. The server already narrows a connection
// to that set and refuses any other kb, so nothing is written into the folder
// and no client needs a project-level override.
//
// ?kbs= is a scope filter, not a security boundary: the token in the session's
// environment can reach every KB it allows. Hard isolation needs a per-workspace
// token (D44/D45).
package mcpbridge

import (
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// Scope is the KB set one session is restricted to.
type Scope struct {
	// All is true when nothing restricts the session: the provider is unbound
	// and has no explicit binding, so the whole routed endpoint is served and no
	// ?kbs= is sent. Empty KBs with All false means NO KB, never every KB.
	All bool
	// KBs is the sorted, deduplicated set, restricted to what the server mounts
	// when that list is known.
	KBs []string
	// Source says where the set came from, for logs and `workspace resolve`:
	// "workspace <path>", "roots", "provider default", "provider binding".
	Source string
	// Binding is the workspace binding that won, when one did.
	Binding *clientconfig.WorkspaceBinding
	// Dirs are, for a scope from roots, the root folders that bound each KB:
	// the repositories whose git identity authors its writes (D904). Nil for
	// any other scope, where the session's own folder stands for every KB.
	Dirs map[string][]string
	// Err is a binding that could not be honoured (bound directory gone, git
	// remote changed). The scope is then empty: failing open here would hand a
	// customer's folder another perimeter's KBs.
	Err error
}

// None reports whether the session must be served without any KB.
func (s Scope) None() bool { return !s.All && len(s.KBs) == 0 }

// Query is the ?kbs= value for the routed endpoint, "" when the scope is All.
// It is never "" for a restricted scope: callers check None first, because an
// empty kbs means every KB at the server.
func (s Scope) Query() string {
	if s.All || len(s.KBs) == 0 {
		return ""
	}
	escaped := make([]string, len(s.KBs))
	for i, n := range s.KBs {
		escaped[i] = url.QueryEscape(n)
	}
	return "kbs=" + strings.Join(escaped, ",")
}

// Resolve maps a working directory to a scope. dir is the process working
// directory (os.Getwd), never $PWD: OpenCode starts its MCP children with $PWD
// set to $HOME.
func Resolve(cfg *clientconfig.Config, provider, dir string) Scope {
	w, bound, err := cfg.ResolveWorkspace(provider, dir)
	if err != nil {
		return Scope{Source: "workspace lookup", Err: err}
	}
	if bound {
		return Scope{
			KBs:     clientconfig.RoutedBinding(cfg.KnownKBs, w.KBs),
			Source:  "workspace " + w.Path,
			Binding: &w,
		}
	}
	return providerScope(cfg, provider)
}

// providerScope is the unbound answer: the provider's own binding (D169).
func providerScope(cfg *clientconfig.Config, provider string) Scope {
	bound, explicit := cfg.BoundKBs(provider)
	if !explicit {
		return Scope{All: true, Source: "provider default"}
	}
	return Scope{KBs: clientconfig.RoutedBinding(cfg.KnownKBs, bound), Source: "provider binding"}
}

// ResolveRoots is the fallback for a client that declares MCP roots while the
// working directory is bound to nothing: the union of the bindings the roots
// resolve to. ok is false when no root is bound, and the caller keeps the
// provider scope.
func ResolveRoots(cfg *clientconfig.Config, provider string, roots []string) (Scope, bool) {
	dirs := map[string][]string{}
	var firstErr error
	hit := false
	for _, r := range roots {
		dir, ok := rootPath(r)
		if !ok {
			continue
		}
		w, bound, err := cfg.ResolveWorkspace(provider, dir)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !bound {
			continue
		}
		hit = true
		for _, kb := range clientconfig.RoutedBinding(cfg.KnownKBs, w.KBs) {
			dirs[kb] = append(dirs[kb], dir)
		}
	}
	if !hit {
		if firstErr != nil {
			return Scope{Source: "roots", Err: firstErr}, true
		}
		return Scope{}, false
	}
	kbs := make([]string, 0, len(dirs))
	for kb := range dirs {
		kbs = append(kbs, kb)
	}
	sort.Strings(kbs)
	return Scope{KBs: kbs, Source: "roots", Dirs: dirs}, true
}

// rootPath turns a file:// root URI into a local path. Windows URIs look like
// file:///C:/Users/x, whose path component is /C:/Users/x.
func rootPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return "", false
	}
	p := u.Path
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	if u.Host != "" && u.Host != "localhost" {
		// A UNC share: file://server/share/dir.
		p = "//" + u.Host + p
	}
	if p == "" {
		return "", false
	}
	return filepath.FromSlash(p), true
}

// Describe is the one-line human form of a scope.
func (s Scope) Describe() string {
	switch {
	case s.Err != nil:
		return fmt.Sprintf("no KB (%s)", s.Err)
	case s.All:
		return "every KB of the provider (" + s.Source + ")"
	case len(s.KBs) == 0:
		return "no KB (" + s.Source + ")"
	default:
		return strings.Join(s.KBs, ", ") + " (" + s.Source + ")"
	}
}
