package provisioning

import (
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// PluginName returns the plugin of a plugin source, or "" for anything else.
func PluginName(s string) string {
	if !contract.IsPluginSource(s) {
		return ""
	}
	return strings.TrimPrefix(s, contract.PluginSourcePrefix)
}

// PluginCollision is a kind and name claimed by a plugin and by at least one
// other source.
type PluginCollision struct {
	Kind, Name string
	Sources    []string // sorted
}

// DropPluginCollisions resolves every kind+name that a plugin shares with
// another source, and reports each one. A bundled artifact always stays: the
// plugin claimants of that name are dropped (a plugin named like a bundled skill
// would otherwise make the sync prune the bundled one). Against a KB or another
// plugin there is no winner: every claimant is dropped, because a silent winner
// would change when a plugin is added or removed. KB against KB is not its
// business: MergeArtifactsStrict keeps refusing it. Callers run it before
// MergeArtifactsStrict. The same source repeated is not a collision (the per-KB
// merge carries the bundled artifacts once per KB).
func DropPluginCollisions(artifacts []contract.Artifact) (kept []contract.Artifact, dropped []PluginCollision) {
	type key struct{ kind, name string }
	sources := map[key]map[string]bool{}
	for _, a := range artifacts {
		k := key{a.Kind, a.Name}
		if sources[k] == nil {
			sources[k] = map[string]bool{}
		}
		sources[k][a.Source] = true
	}
	drop := map[key]bool{}       // every claimant goes
	dropPlugin := map[key]bool{} // only the plugin claimants go (a bundled one stays)
	for k, set := range sources {
		if len(set) < 2 {
			continue
		}
		hasPlugin := false
		list := make([]string, 0, len(set))
		for s := range set {
			hasPlugin = hasPlugin || contract.IsPluginSource(s)
			list = append(list, s)
		}
		if !hasPlugin {
			continue
		}
		sort.Strings(list)
		if set["bundle"] {
			dropPlugin[k] = true
		} else {
			drop[k] = true
		}
		dropped = append(dropped, PluginCollision{Kind: k.kind, Name: k.name, Sources: list})
	}
	sort.Slice(dropped, func(i, j int) bool {
		if dropped[i].Kind != dropped[j].Kind {
			return dropped[i].Kind < dropped[j].Kind
		}
		return dropped[i].Name < dropped[j].Name
	})
	for _, a := range artifacts {
		k := key{a.Kind, a.Name}
		if drop[k] || (dropPlugin[k] && contract.IsPluginSource(a.Source)) {
			continue
		}
		kept = append(kept, a)
	}
	return kept, dropped
}
