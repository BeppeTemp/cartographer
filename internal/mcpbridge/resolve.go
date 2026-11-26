// Package mcpbridge is the local stdio MCP server scoped to a folder's KBs
// (D737): one agent session gets the KBs of the folder it was started in.
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
	// Transversal are the KBs added to this session because the client file
	// declares them transversal (knowledge_bases.<kb>.transversal) and the
	// folder's binding does not carry them, sorted. They are in KBs and in
	// ReadOnlyKBs: instructions and read access everywhere, never a write tool
	// outside a binding. For Describe and logs.
	Transversal []string
	// Unbound is true for the two provider scopes: the folder is under no
	// binding, whatever the access mode. False for a binding, an error and the
	// roots scope.
	Unbound bool
	// AccessOrigin says where an unbound scope's mode comes from: "default"
	// (nothing sets it), "defaults.unbound_access" or
	// "providers.<provider>.unbound_access". Empty for a scope that is not unbound.
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
		ro := intersect(cfg.ReadOnlyKBs(), kbs)
		// A binding with no KB (--no-kb) stays closed: an explicit binding wins
		// over a derived one, the person's declaration included.
		var added []string
		if len(kbs) > 0 {
			kbs, ro, added = withTransversal(cfg, provider, kbs, ro)
		}
		return Scope{
			KBs:         kbs,
			ReadOnlyKBs: ro,
			Transversal: added,
			Source:      "workspace " + w.Path,
			Binding:     &w,
		}
	}
	if cfg.BridgeIsHTTPEquivalent(provider) {
		return pluginOnlyScope(cfg, provider)
	}
	return providerScope(cfg, provider)
}

// pluginOnlyScope is the scope of a provider with no workspace binding and no
// access profile whose session runs in the bridge. It is decided from that, not
// from the plugin state: the entry changes only at the next sync, so after
// `plugin disable` or `remove` the bridge entry outlives the plugin and must
// not turn read-only meanwhile. It is exactly what its HTTP entry gave (the
// provider's own KB set, read-write, kb_options read-only KBs kept), so
// switching the entry to the bridge changes nothing a session can do. It is
// not Unbound: no roots question and no "unbound folder" note, and
// unbound_access is not applied (it never was for a provider on the HTTP entry).
// Do not route this through providerScope: that scope is read-only by default
// and would silently make every such session read-only.
func pluginOnlyScope(cfg *clientconfig.Config, provider string) Scope {
	bound, explicit := cfg.BoundKBs(provider)
	if !explicit {
		return Scope{All: true, ReadOnlyKBs: cfg.ReadOnlyKBs(), Source: "provider default"}
	}
	kbs := clientconfig.RoutedBinding(cfg.KnownKBs, bound)
	return Scope{KBs: kbs, ReadOnlyKBs: intersect(cfg.ReadOnlyKBs(), kbs), Source: "provider binding"}
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
		// The whole set is served, the transversal KBs in it: in write mode
		// they are named read-only, as everywhere outside a binding.
		ros, added := cfg.ReadOnlyKBs(), []string(nil)
		if !ro {
			ros, added = withReadOnly(ros, cfg.TransversalKBs(provider))
		}
		return Scope{All: true, ReadOnly: ro, ReadOnlyKBs: readOnlyUnless(ro, ros), Transversal: added, Unbound: true, AccessOrigin: origin, Source: "provider default"}
	}
	kbs := clientconfig.RoutedBinding(cfg.KnownKBs, bound)
	roKBs := intersect(cfg.ReadOnlyKBs(), kbs)
	kbs, roKBs, added := withTransversal(cfg, provider, kbs, roKBs)
	return Scope{KBs: kbs, ReadOnly: ro, ReadOnlyKBs: readOnlyUnless(ro, roKBs), Transversal: added, Unbound: true, AccessOrigin: origin, Source: "provider binding"}
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
	ro := intersect(cfg.ReadOnlyKBs(), kbs)
	kbs, ro, added := withTransversal(cfg, provider, kbs, ro)
	return Scope{KBs: kbs, ReadOnlyKBs: ro, Transversal: added, Source: "roots", Dirs: dirs}, true
}

// withoutNames is list minus drop, in order.
func withoutNames(list, drop []string) []string {
	var out []string
	for _, n := range list {
		if !containsName(drop, n) {
			out = append(out, n)
		}
	}
	return out
}

// withReadOnly is ro with names added, sorted and deduplicated; added is the
// names that were not in it.
func withReadOnly(ro, names []string) (out, added []string) {
	out = append([]string(nil), ro...)
	for _, n := range names {
		if !containsName(out, n) {
			out = append(out, n)
			added = append(added, n)
		}
	}
	sort.Strings(out)
	return out, added
}

func containsName(list []string, n string) bool {
	for _, m := range list {
		if m == n {
			return true
		}
	}
	return false
}

// withTransversal adds to a session's effective KB set the transversal KBs of
// provider that the set does not already carry, and names each added one
// read-only. It is applied to the effective set (the binding's KBs with their
// lineage, the provider's BoundKBs), never to the own lists that seed a
// lineage closure, so a transversal KB is never an input of one. A KB already
// in the set keeps the access the folder gives it. kbs and ro are sorted and
// deduplicated; added is the names put in.
func withTransversal(cfg *clientconfig.Config, provider string, kbs, ro []string) (outKBs, outRO, added []string) {
	have := map[string]bool{}
	for _, k := range kbs {
		have[k] = true
	}
	for _, k := range cfg.TransversalKBs(provider) {
		if !have[k] {
			added = append(added, k)
		}
	}
	if len(added) == 0 {
		return kbs, ro, nil
	}
	outKBs = append(append([]string(nil), kbs...), added...)
	sort.Strings(outKBs)
	outRO = append(append([]string(nil), ro...), added...)
	sort.Strings(outRO)
	return outKBs, outRO, added
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
	// The added KBs are named once, on the `+` part, not as members of the set.
	set := s
	if own := withoutNames(s.KBs, s.Transversal); len(own) > 0 {
		set.KBs = own
	} else if len(s.Transversal) > 0 && !s.All {
		// Nothing but the added KBs: the set is "no KB" of its own.
		set.KBs = nil
	}
	d := set.describeSet()
	if s.Err == nil && len(s.Transversal) > 0 {
		d += "; + " + strings.Join(s.Transversal, ", ") + " read-only (transversal)"
	}
	return d
}

func (s Scope) describeSet() string {
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
