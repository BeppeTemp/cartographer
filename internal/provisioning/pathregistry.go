package provisioning

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/repoindex"
)

// PathDecl is one key a KB declares in its paths.yaml (D263), as sync_pull's
// `path_registry` field carries it. It mirrors kb.PathDecl's JSON tags — the
// wire format — without importing the server's data plane into the client.
// Remote, for a repo key, is already normalized by the server.
type PathDecl struct {
	Description string `json:"description"`
	Default     string `json:"default,omitempty"`
	Remote      string `json:"remote,omitempty"`
}

// PathRegistry is one KB's declared placeholder vocabulary (D263): `paths`
// declares {{path:<key>}} keys, `repos` {{repo:<key>}} keys. Unsigned KB
// content, like a concept body: it can only propose a home-anchored path, only
// as a fallback, and only one that exists — it never executes anything.
type PathRegistry struct {
	Paths map[string]PathDecl `json:"paths,omitempty"`
	Repos map[string]PathDecl `json:"repos,omitempty"`
}

// byID flattens the registry to "kind:key" -> declaration.
func (r PathRegistry) byID() map[string]PathDecl {
	out := make(map[string]PathDecl, len(r.Paths)+len(r.Repos))
	for k, d := range r.Paths {
		out["path:"+k] = d
	}
	for k, d := range r.Repos {
		out["repo:"+k] = d
	}
	return out
}

// MergePathRegistries folds the registries of the KBs bound to one projection
// into one "kind:key" -> declaration map, plus the kbs declaring each key.
// order is the provider's explicit KB order (D182); KBs it does not name
// follow alphabetically. When two KBs declare one key with a different
// default or remote, the first in that order wins and a warning names both —
// never an error: resolution must not block a sync (D75 WP3).
func MergePathRegistries(order []string, regs map[string]PathRegistry) (map[string]PathDecl, map[string][]string, []string) {
	if len(regs) == 0 {
		return nil, nil, nil
	}
	var kbs []string
	listed := map[string]bool{}
	for _, kb := range order {
		if _, ok := regs[kb]; ok && !listed[kb] {
			listed[kb] = true
			kbs = append(kbs, kb)
		}
	}
	var rest []string
	for kb := range regs {
		if !listed[kb] {
			rest = append(rest, kb)
		}
	}
	sort.Strings(rest)
	kbs = append(kbs, rest...)

	decls := map[string]PathDecl{}
	declaredBy := map[string][]string{}
	winner := map[string]string{}
	var warnings []string
	for _, kb := range kbs {
		byID := regs[kb].byID()
		ids := make([]string, 0, len(byID))
		for id := range byID {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			d := byID[id]
			if kb != "" {
				declaredBy[id] = append(declaredBy[id], kb)
			}
			prev, seen := decls[id]
			if !seen {
				decls[id] = d
				winner[id] = kb
				continue
			}
			if prev.Default != d.Default || prev.Remote != d.Remote {
				warnings = append(warnings, fmt.Sprintf(
					"placeholder %s is declared differently by KB %q (%s) and KB %q (%s); using %q's, the first in this client's KB order",
					id, winner[id], describeDecl(prev), kb, describeDecl(d), winner[id]))
			}
		}
	}
	return decls, declaredBy, warnings
}

func describeDecl(d PathDecl) string {
	var parts []string
	if d.Default != "" {
		parts = append(parts, "default "+d.Default)
	}
	if d.Remote != "" {
		parts = append(parts, "remote "+d.Remote)
	}
	if len(parts) == 0 {
		return "no default"
	}
	return strings.Join(parts, ", ")
}

// registryDefaultPath turns a declared default into this machine's path.
// The server validated it to be "~", "$HOME" or either followed by "/…" with
// no ".." segment; it is checked again here because the registry is unsigned
// KB content and the trust statement of D263 — a default can only point under
// the reader's home — must hold whatever server sent it. ok is false for
// anything else, which then counts as no default at all.
func registryDefaultPath(p string) (string, bool) {
	var rest string
	switch {
	case p == "~" || p == "$HOME":
	case strings.HasPrefix(p, "~/"):
		rest = p[2:]
	case strings.HasPrefix(p, "$HOME/"):
		rest = p[6:]
	default:
		return "", false
	}
	for _, seg := range strings.Split(rest, "/") {
		if seg == ".." {
			return "", false
		}
	}
	if rest == "" {
		return repoindex.ExpandHome("~"), true
	}
	return repoindex.ExpandHome("~/" + rest), true
}
