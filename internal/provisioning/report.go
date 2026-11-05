package provisioning

import (
	"path"
	"path/filepath"
	"sort"
	"strings"
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
func ReportNotes(notes []ProjectionNote) (degraded []DegradedNote, skipped []SkippedNote) {
	for _, n := range notes {
		switch n.Kind {
		case NoteDegraded:
			_, name, _ := strings.Cut(n.Artifact, "/")
			degraded = append(degraded, DegradedNote{Artifact: n.Artifact, As: "skill/" + name})
		case NoteDegradedSkipped:
			skipped = append(skipped, SkippedNote{Artifact: n.Artifact, Reason: "not installed as a skill (" + n.Reason + ")"})
		case NoteSkipped:
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
func unverifiedCapabilities(m Manifest, opts ApplyOptions) []Capability {
	set := map[Capability]bool{}
	for _, a := range m.Artifacts {
		if a.BuiltIn || a.DerivedFrom != nil || destDirScoped(a.Kind, a.Name, opts.Provider, opts.Scope) == "" || !artifactAuthorized(a, opts) {
			continue
		}
		switch a.Kind {
		case "agent":
			if strictAgentSkipped(a, opts.Provider) {
				continue
			}
			d, i := CapabilityStatus(opts.Provider, opts.Scope, CapAgentDelegate), CapabilityStatus(opts.Provider, opts.Scope, CapAgentInvoke)
			if d == CapUnverified && i != CapVerified {
				set[CapAgentDelegate] = true
			}
			if i == CapUnverified && d != CapVerified {
				set[CapAgentInvoke] = true
			}
		case "hook":
			if isReservedHook(a.Kind, a.Name) {
				continue
			}
			files := a.Files
			if len(files) == 0 {
				var err error
				if files, err = ReadArtifactFiles(a, opts.BundleFS, opts.KBRoots); err != nil {
					continue
				}
			}
			for _, f := range files {
				if path.Clean(filepath.ToSlash(f.Path)) != "hook.json" {
					continue
				}
				spec, err := parseHookSpec(f.Content)
				if err != nil || !hookEventKnown(spec.Event) {
					break
				}
				id := Capability(hookCapPrefix + spec.Event)
				if CapabilityStatus(opts.Provider, opts.Scope, id) == CapUnverified {
					set[id] = true
				}
				break
			}
		}
	}
	out := make([]Capability, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
