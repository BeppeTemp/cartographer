package provisioning

import (
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// What a sync reports about the decisions of the resolver (D1656): the agents
// installed as skills, the artifacts left out because of a requirement, and the
// capabilities an installed artifact depends on that nobody probed.

// DegradedNote says a KB agent installs as a skill on this client.
type DegradedNote struct {
	Artifact string // "agent/<name>"
	As       string // "skill/<name>"
}

// SkippedNote says an artifact is not installed on this client, with the cause
// chain the resolver recorded.
type SkippedNote struct {
	Artifact string // "kind/<name>"
	Reason   string
}

// ReportNotes splits the notes of a ProjectionResult into the two lists
// AppliedResult carries. An agent that would degrade but cannot (a real skill
// of that name, no description) is reported as skipped: it is not installed.
// Both lists keep the order of the notes, which the resolver makes
// deterministic.
func ReportNotes(notes []contract.ProjectionNote) (degraded []DegradedNote, skipped []SkippedNote) {
	for _, n := range notes {
		switch n.Kind {
		case contract.NoteDegraded:
			_, name, _ := strings.Cut(n.Artifact, "/")
			degraded = append(degraded, DegradedNote{Artifact: n.Artifact, As: "skill/" + name})
		case contract.NoteDegradedSkipped:
			skipped = append(skipped, SkippedNote{Artifact: n.Artifact, Reason: "not installed as a skill (" + n.Reason + ")"})
		case contract.NoteSkipped:
			skipped = append(skipped, SkippedNote{Artifact: n.Artifact, Reason: n.Reason})
		}
	}
	return degraded, skipped
}

// unverifiedCapabilities lists, sorted, the capabilities of the table that an
// authorized, installed artifact of m depends on while their status is
// `unverified`: both agent capabilities for a native agent when neither is
// verified, and the `hook.<Event>` of a hook. Skill and mcp cells are not
// reported: every sync installs them, so the line would never go away.
func unverifiedCapabilities(m contract.Manifest, opts ApplyOptions) []contract.Capability {
	set := map[contract.Capability]bool{}
	for _, a := range m.Artifacts {
		if a.BuiltIn || a.DerivedFrom != nil || contract.DestDirScoped(a.Kind, a.Name, opts.Provider, opts.Scope) == "" || !artifactAuthorized(a, opts) {
			continue
		}
		for _, id := range contract.UnverifiedFor(a, opts.Provider, opts.Scope, opts.BundleFS, opts.KBRoots) {
			set[id] = true
		}
	}
	out := make([]contract.Capability, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
