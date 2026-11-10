package contract

import (
	"regexp"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// What each client does with each artifact of a KB (D1656), for the Atlas: the
// same pure functions a sync runs (AgentMode, the resolver), over the KB's own
// artifact set, in the global scope.

// Client modes.
const (
	// ModeNative: installed as it is, its capabilities probed or not needed.
	ModeNative = "native"
	// ModeDegraded: an agent installed as a skill.
	ModeDegraded = "degraded"
	// ModeSkipped: not installed, Reason says why.
	ModeSkipped = "skipped"
	// ModeUnverified: installed, but it depends on a capability nobody probed,
	// or on an artifact of another KB the Atlas cannot see.
	ModeUnverified = "unverified"
)

// ClientMode is what one client does with one artifact.
type ClientMode struct {
	Provider configurator.Provider
	Mode     string
	Reason   string
}

// externalRef finds the artifacts a cause chain reports as provided by nobody.
var externalRef = regexp.MustCompile(`requires (\w+)/(\S+) \(not provided to this client\)`)

// ClientModes returns, by "kind/name", the clients that receive each artifact
// of arts and in which mode, in registry order. An artifact with no
// destination anywhere has no entry. A requirement on an artifact outside arts
// is not a skip here: the artifact may come from another KB bound to the same
// client, so it is unverified with the reason "depends on another KB".
func ClientModes(arts []Artifact, opts ProjectOptions) map[string][]ClientMode {
	inKB := map[string]bool{}
	for _, a := range arts {
		inKB[a.Kind+"/"+a.Name] = true
	}
	out := map[string][]ClientMode{}
	for _, d := range configurator.Providers() {
		const scope = ScopeGlobal
		key := string(d.Provider)
		res := ResolveProjections([]ProjectionInput{{Key: key, Provider: d.Provider, Scope: scope, Manifest: Manifest{Artifacts: arts}}}, opts)[key]
		alive := map[string]bool{}
		derived := map[string]bool{}
		for _, a := range res.Manifest.Artifacts {
			if o, ok := a.Origin(); ok {
				derived[o.Kind+"/"+o.Name] = true
				alive[o.Kind+"/"+o.Name] = true
				continue
			}
			alive[a.Kind+"/"+a.Name] = true
		}
		skipped := map[string]string{}
		for _, n := range res.Notes {
			switch n.Kind {
			case NoteSkipped, NoteDegradedSkipped:
				skipped[n.Artifact] = n.Reason
			}
		}
		for _, a := range arts {
			id := a.Kind + "/" + a.Name
			m := ClientMode{Provider: d.Provider}
			switch reason, isSkipped := skipped[id]; {
			case isSkipped:
				if external(reason, inKB) {
					m.Mode, m.Reason = ModeUnverified, "depends on another KB"
				} else {
					m.Mode, m.Reason = ModeSkipped, reason
				}
			case derived[id]:
				m.Mode = ModeDegraded
			case alive[id]:
				m.Mode = ModeNative
				if len(UnverifiedFor(a, d.Provider, scope, opts.BundleFS, opts.KBRoots)) > 0 {
					m.Mode = ModeUnverified
				}
			case StrictAgentSkipped(a, d.Provider) && AgentMode(d.Provider, scope) == AgentDegraded && DestDirScoped("skill", a.Name, d.Provider, scope) != "":
				m.Mode, m.Reason = ModeSkipped, "strict_tools"
			default:
				continue
			}
			out[id] = append(out[id], m)
		}
	}
	return out
}

// external reports whether a skip reason is only that a required artifact is
// not in the KB's own set.
func external(reason string, inKB map[string]bool) bool {
	found := false
	for _, m := range externalRef.FindAllStringSubmatch(reason, -1) {
		if !inKB[m[1]+"/"+strings.TrimRight(m[2], ")")] {
			found = true
		}
	}
	return found
}

// KindClientModes is the kind-level list: the providers that receive the kind,
// and for agents also the ones that receive them as skills.
func KindClientModes(kind string) []ClientMode {
	var out []ClientMode
	for _, d := range configurator.Providers() {
		switch {
		case DestDir(kind, "", d.Provider) != "" && (kind != "agent" || AgentMode(d.Provider, ScopeGlobal) == AgentNative):
			out = append(out, ClientMode{Provider: d.Provider})
		case kind == "agent" && DestDirScoped("skill", "x", d.Provider, ScopeGlobal) != "":
			out = append(out, ClientMode{Provider: d.Provider, Mode: ModeDegraded})
		}
	}
	return out
}
