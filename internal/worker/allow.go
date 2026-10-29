package worker

import (
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// denyTools are the tools the model never sees and never calls, whatever
// allowlist the caller passes (D9). Beyond the four D9 names the deny set
// carries the tools that would let an injected prompt widen its own reach or
// leak secrets. Matching is by exact name, plus the artifact_ prefix.
var denyTools = []string{
	"concept_delete", "repair_revert", "concept_transfer",
	"secret_resolve", "secret_set", "skill_install", "git_conflict_resolve",
}

const denyPrefix = "artifact_"

// readTools are the default allowlist's read-only tools; a made call to
// anything else counts as a write in the Outcome.
var readTools = []string{
	"atlas_overview", "search", "concept_read", "concept_list", "concept_history",
	"changes_since", "graph_context", "graph_neighbors", "graph_path", "link_suggest",
	"work_list", "kb_guide", "kb_status", "index_get", "map_list", "source_list",
	"template_list", "asset_list", "asset_read", "log_tail", "lint", "gate_check",
	"contradiction_report",
}

// writeTools are the default allowlist's content-writing tools.
var writeTools = []string{
	"concept_write", "concept_patch", "concept_new", "concept_batch", "concept_move",
	"concept_archive", "concept_merge", "index_patch", "map_create", "map_update",
	"source_register", "log_append", "asset_write", "supersede",
}

// Denied reports whether name is in the hard deny set.
func Denied(name string) bool {
	if strings.HasPrefix(name, denyPrefix) {
		return true
	}
	for _, d := range denyTools {
		if d == name {
			return true
		}
	}
	return false
}

// DefaultTools returns the default allowlist (read tools and content writes).
func DefaultTools() []string { return append(append([]string{}, readTools...), writeTools...) }

// DenyTools returns the exact names of the deny set (the artifact_ prefix is
// applied on top).
func DenyTools() []string { return append([]string{}, denyTools...) }

// Allowlist decides which tools the model can see or call; this file is the
// only place that does. A tool not named is not offered, so a tool the server
// adds later stays invisible to workers until someone lists it.
type Allowlist struct{ names map[string]bool }

// NewAllowlist starts from names (the default list when none) and always
// subtracts the deny set.
func NewAllowlist(names ...string) *Allowlist {
	if len(names) == 0 {
		names = DefaultTools()
	}
	a := &Allowlist{names: map[string]bool{}}
	for _, n := range names {
		if !Denied(n) {
			a.names[n] = true
		}
	}
	return a
}

// Permits reports whether name may ever be offered or called.
func (a *Allowlist) Permits(name string) bool { return a.names[name] && !Denied(name) }

// Filter is the intersection of what the server lists and what is allowed,
// sorted by name. An allowed tool the server does not list (the token cannot
// see it) is simply absent.
func (a *Allowlist) Filter(listed []client.ToolInfo) []ToolSpec {
	var out []ToolSpec
	for _, t := range listed {
		if a.Permits(t.Name) {
			out = append(out, ToolSpec{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func isRead(name string) bool {
	for _, r := range readTools {
		if r == name {
			return true
		}
	}
	return false
}
