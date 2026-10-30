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
	// ReadOnly is true for a folder no workspace binding covers, unless the
	// unbound_access setting says write (#1356): the session keeps the
	// provider's set but asks the server for read tools only (?access=read,
	// #1018). A workspace binding that wins is never read-only.
	ReadOnly bool
	// ReadOnlyKBs are the KBs of this session set read-only on the client
	// (kb_options.<kb>.access: read, #1409), sorted. They go to the server as
	// readonly=, which narrows only. Empty for a ReadOnly scope: access=read
	// already covers every KB.
	ReadOnlyKBs []string
	// Unbound is true for the two provider scopes: the folder is under no
	// binding, whatever the access mode. False for a binding, an error and the
	// roots scope.
	Unbound bool
	// AccessOrigin says where an unbound scope's mode comes from: "default" or
	// "unbound_access". Empty for a scope that is not unbound.
	AccessOrigin string
	// Err is a binding that could not be honoured (bound directory gone, git
	// remote changed). The scope is then empty: failing open here would hand a
	// customer's folder another perimeter's KBs.
	Err error
}

// None reports whether the session must be served without any KB.
func (s Scope) None() bool { return !s.All && len(s.KBs) == 0 }

// Query is the query string for the routed endpoint: ?kbs= for a restricted
// scope, plus access=read for a read-only one, else readonly=<names> for the KBs
// set read-only (#1409; after access=, never with it). It is "" only for an All
// scope that is not read-only and has no read-only KB. An All scope sends no
// kbs=, but still sends readonly=. kbs is never sent empty for a restricted scope:
// callers check None first, because an empty kbs means every KB at the server.
func (s Scope) Query() string {
	var parts []string
	if !s.All && len(s.KBs) > 0 {
		escaped := make([]string, len(s.KBs))
		for i, n := range s.KBs {
			escaped[i] = url.QueryEscape(n)
		}
		parts = append(parts, "kbs="+strings.Join(escaped, ","))
	}
	if s.ReadOnly {
		parts = append(parts, "access=read")
	} else if len(s.ReadOnlyKBs) > 0 {
		escaped := make([]string, len(s.ReadOnlyKBs))
		for i, n := range s.ReadOnlyKBs {
			escaped[i] = url.QueryEscape(n)
		}
		parts = append(parts, "readonly="+strings.Join(escaped, ","))
	}
	return strings.Join(parts, "&")
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
		kbs := clientconfig.RoutedBinding(cfg.KnownKBs, w.KBs)
		return Scope{
			KBs:         kbs,
			ReadOnlyKBs: intersect(cfg.ReadOnlyKBs(), kbs),
			Source:      "workspace " + w.Path,
			Binding:     &w,
		}
	}
	return providerScope(cfg, provider)
}

// providerScope is the unbound answer: the provider's own binding (D169),
// read-only unless unbound_access says write (#1356), because a folder under no
// binding receives no KB artifact and a session with write tools and none of
// the skills that explain them is the failure the default prevents.
func providerScope(cfg *clientconfig.Config, provider string) Scope {
	mode, origin := cfg.UnboundAccess(provider)
	ro := mode != "write"
	bound, explicit := cfg.BoundKBs(provider)
	if !explicit {
		return Scope{All: true, ReadOnly: ro, ReadOnlyKBs: readOnlyUnless(ro, cfg.ReadOnlyKBs()), Unbound: true, AccessOrigin: origin, Source: "provider default"}
	}
	kbs := clientconfig.RoutedBinding(cfg.KnownKBs, bound)
	return Scope{KBs: kbs, ReadOnly: ro, ReadOnlyKBs: readOnlyUnless(ro, intersect(cfg.ReadOnlyKBs(), kbs)), Unbound: true, AccessOrigin: origin, Source: "provider binding"}
}

// ResolveRoots is the fallback for a client that declares MCP roots while the
// working directory is bound to nothing: the union of the bindings the roots
// resolve to. ok is false when no root is bound, and the caller keeps the
// provider scope, which is read-only.
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
	return Scope{KBs: kbs, ReadOnlyKBs: intersect(cfg.ReadOnlyKBs(), kbs), Source: "roots", Dirs: dirs}, true
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
	case s.All && s.ReadOnly:
		return "every KB of the provider, read-only (" + s.Source + ")"
	case s.All && s.Unbound:
		return "every KB of the provider (" + s.Source + ", unbound)"
	case s.All:
		return "every KB of the provider (" + s.Source + ")"
	case len(s.KBs) == 0:
		return "no KB (" + s.Source + ")"
	case s.ReadOnly:
		return strings.Join(s.KBs, ", ") + " read-only (" + s.Source + ")"
	case s.Unbound:
		return strings.Join(s.KBs, ", ") + " (" + s.Source + ", unbound)"
	default:
		return strings.Join(s.KBs, ", ") + " (" + s.Source + ")"
	}
}

// intersect returns the names of a that are in b, in a's order.
func intersect(a, b []string) []string {
	var out []string
	for _, n := range a {
		for _, m := range b {
			if n == m {
				out = append(out, n)
				break
			}
		}
	}
	return out
}

// readOnlyUnless drops the names of a scope that is read-only as a whole.
func readOnlyUnless(readOnly bool, names []string) []string {
	if readOnly {
		return nil
	}
	return names
}
