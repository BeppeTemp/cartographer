package provisioning

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// ProjectManifest is the client-side projection step: it replaces the filter
// that kept only the artifacts a provider can materialize in a scope. It keeps
// what FilterForProviderScoped keeps and, for each KB agent the client cannot
// run as a subagent, appends a derived skill (D1656, WP4). Requirement
// evaluation arrives behind this same function.
//
// A derived skill is a verified artifact transformed locally: it runs after
// VerifiedManifest, copies Signed/BuiltIn/Signature from its origin and keeps
// the origin in DerivedFrom, which approval and provenance resolve to.

// DerivedFrom is the origin of a skill derived from an agent. Client-side
// only: it never travels on the wire and is outside every signature.
type DerivedFrom struct {
	Kind        string
	Name        string
	ContentHash string
	// Files are the origin's files, so approval is judged on what the KB
	// shipped (the derived frontmatter has no hooks/MCP keys, and a gated agent
	// must not install ungated as a skill).
	Files       []ArtifactFile
	Restriction *AgentRestriction
}

// Origin returns the artifact a derived one comes from, as it was before the
// derivation, and ok=false for an artifact that is not derived.
func (a Artifact) Origin() (Artifact, bool) {
	d := a.DerivedFrom
	if d == nil {
		return a, false
	}
	o := a
	o.Kind, o.Name, o.ContentHash = d.Kind, d.Name, d.ContentHash
	o.Files, o.Restriction, o.DerivedFrom = d.Files, d.Restriction, nil
	return o, true
}

// ProjectOptions says where a projection finds the content of an artifact
// whose Files are not in memory.
type ProjectOptions struct {
	BundleFS fs.FS
	KBRoots  map[string]string
}

// Projection note kinds.
const (
	// NoteDegraded: an agent installs as a skill on this client.
	NoteDegraded = "degraded"
	// NoteDegradedSkipped: an agent would degrade but does not (a real skill
	// of that name, or no description).
	NoteDegradedSkipped = "degraded-skipped"
	// NoteSkipped: an artifact is left out because a requirement of it is not
	// met on this client; Reason is the cause chain.
	NoteSkipped = "skipped"
)

// ProjectionNote reports one decision of ProjectManifest.
type ProjectionNote struct {
	Kind     string // NoteDegraded | NoteDegradedSkipped | NoteSkipped
	Artifact string // "agent/<name>"
	Reason   string
}

// NoWorkspaceDestinationReason is the reason of the note for a kind that a
// client carries in the global scope and has no project destination for.
func NoWorkspaceDestinationReason(kind string) string {
	if kind == "hook" {
		return "no workspace destination for this client: hooks run only in the global scope"
	}
	return "no workspace destination for this client: " + kind + "s exist only in the global scope"
}

// WorkspaceLacksKinds lists the artifact kinds that provider carries in the
// global scope and has no workspace destination for, the ones projectOne
// reports as skipped. An agent a workspace degrades to a skill is not one.
func WorkspaceLacksKinds(provider configurator.Provider) []string {
	var out []string
	for _, kind := range artifactKinds {
		if destDirScoped(kind, "x", provider, ScopeProject) != "" || destDirScoped(kind, "x", provider, ScopeGlobal) == "" {
			continue
		}
		if kind == "agent" && destDirScoped("skill", "x", provider, ScopeProject) != "" {
			continue
		}
		out = append(out, kind)
	}
	return out
}

// degradedTag versions the derivation: a change of it changes every derived
// hash, so the skills are rewritten.
const degradedTag = "degraded-v1"

// degradedHeader is the line that tells the model what this skill is.
const degradedHeader = "> Degraded subagent: this client has no subagents, so this runs in the main context, with no separate context and no tool restriction."

// ProjectManifest returns what provider receives in scope from m, and one
// note per agent that was degraded or could not be.
func ProjectManifest(m Manifest, provider configurator.Provider, scope Scope, opts ProjectOptions) (Manifest, []ProjectionNote) {
	out, notes, _ := projectOne(m, provider, scope, opts)
	return out, notes
}

// projectOne is ProjectManifest plus the reason each candidate that did not
// make it into the projection was left out, by "kind/name": the resolver
// quotes it when something requires that artifact.
func projectOne(m Manifest, provider configurator.Provider, scope Scope, opts ProjectOptions) (Manifest, []ProjectionNote, map[string]string) {
	dropped := map[string]string{}
	var out Manifest
	var notes []ProjectionNote
	var degradable []Artifact
	derived := false
	haveSkill := map[string]bool{}
	nativeAgents := AgentMode(provider, scope) == AgentNative
	for _, a := range m.Artifacts {
		if destDirScoped(a.Kind, a.Name, provider, scope) != "" && !strictAgentSkipped(a, provider) && (a.Kind != "agent" || nativeAgents) {
			out.Artifacts = append(out.Artifacts, a)
			if a.Kind == "skill" {
				haveSkill[a.Name] = true
			}
			continue
		}
		id := a.Kind + "/" + a.Name
		switch {
		case strictAgentSkipped(a, provider):
			dropped[id] = "strict_tools"
		case a.Kind == "agent" && destDirScoped("skill", a.Name, provider, scope) != "":
			degradable = append(degradable, a)
		default:
			dropped[id] = "no destination"
			if scope == ScopeProject && destDirScoped(a.Kind, a.Name, provider, ScopeGlobal) != "" {
				// The client carries this kind globally but a workspace has no
				// place for it: say so instead of dropping it silently (#1785).
				notes = append(notes, ProjectionNote{NoteSkipped, id, NoWorkspaceDestinationReason(a.Kind)})
			}
		}
	}
	for _, a := range degradable {
		id := "agent/" + a.Name
		if haveSkill[a.Name] {
			notes = append(notes, ProjectionNote{NoteDegradedSkipped, id, "a skill of that name exists"})
			dropped[id] = "name collision"
			continue
		}
		d, reason := degradeAgent(a, opts)
		if reason != "" {
			notes = append(notes, ProjectionNote{NoteDegradedSkipped, id, reason})
			dropped[id] = reason
			continue
		}
		out.Artifacts = append(out.Artifacts, d)
		derived = true
		notes = append(notes, ProjectionNote{NoteDegraded, id, "agent -> skill"})
	}
	// The same order MergeArtifacts gives, which computeRevision needs (D170).
	// Only when something was derived: a projection with nothing to add keeps
	// the order of m, as the filter did, so its revision does not move.
	if derived {
		sort.SliceStable(out.Artifacts, func(i, j int) bool {
			ai, aj := out.Artifacts[i], out.Artifacts[j]
			if ai.Kind != aj.Kind {
				return ai.Kind < aj.Kind
			}
			if ai.Source != aj.Source {
				return ai.Source < aj.Source
			}
			return ai.Name < aj.Name
		})
	}
	out.Revision = computeRevision(out.Artifacts)
	out.Skipped = m.Skipped
	return out, notes, dropped
}

// degradeAgent derives the skill of agent a. A non-empty reason means it
// cannot be derived.
func degradeAgent(a Artifact, opts ProjectOptions) (Artifact, string) {
	files := a.Files
	if len(files) == 0 {
		var err error
		files, err = ReadArtifactFiles(a, opts.BundleFS, opts.KBRoots)
		if err != nil {
			return Artifact{}, "its content cannot be read: " + err.Error()
		}
	}
	if len(files) != 1 {
		return Artifact{}, fmt.Sprintf("expected 1 file, got %d", len(files))
	}
	fmRaw, body, hasFM := okf.SplitFrontmatter(string(files[0].Content))
	if !hasFM {
		return Artifact{}, "it has no description"
	}
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil {
		return Artifact{}, "its frontmatter cannot be parsed"
	}
	v, _ := fm.Get("description")
	desc, _ := v.(string)
	if strings.TrimSpace(desc) == "" {
		return Artifact{}, "it has no description"
	}
	var sb strings.Builder
	sb.WriteString("---\nname: " + yamlQuoteScalar(a.Name) + "\ndescription: " + yamlQuoteScalar(desc) + "\n---\n\n")
	sb.WriteString(degradedHeader + "\n\n")
	sb.WriteString(strings.TrimLeft(body, "\r\n"))
	skillFiles := []ArtifactFile{{Path: "SKILL.md", Content: []byte(sb.String())}}
	h := sha256.Sum256([]byte(degradedTag + "\x00" + a.ContentHash))
	return Artifact{
		Kind:        "skill",
		Name:        a.Name,
		Source:      a.Source,
		Version:     a.Version,
		ContentHash: fmt.Sprintf("%x", h),
		Signed:      a.Signed,
		BuiltIn:     a.BuiltIn,
		Signature:   a.Signature,
		Files:       skillFiles,
		DerivedFrom: &DerivedFrom{Kind: a.Kind, Name: a.Name, ContentHash: a.ContentHash, Files: files, Restriction: a.Restriction},
	}, ""
}

// derivedFromLabel is the ManagedFile.DerivedFrom spelling.
func derivedFromLabel(a Artifact) string {
	if a.DerivedFrom == nil {
		return ""
	}
	return a.DerivedFrom.Kind + "/" + a.DerivedFrom.Name
}

// degradedWidenedWarnings is widenedAgentWarnings for the agents installed as
// skills: they lose their `tools` allow-list like on any non-Claude client.
func degradedWidenedWarnings(m Manifest, opts ApplyOptions) []string {
	var out []string
	for _, a := range m.Artifacts {
		if a.DerivedFrom == nil || a.DerivedFrom.Restriction == nil || a.DerivedFrom.Restriction.Strict {
			continue
		}
		if !artifactAuthorized(a, opts) {
			continue
		}
		out = append(out, fmt.Sprintf(
			"agent %[1]q declares tools: %[2]s; %[3]s has no subagents, so it receives the agent as a degraded skill with no tool restriction (add `strict_tools: true` to skip it there instead)",
			a.Name, a.DerivedFrom.Restriction.Tools, opts.Provider))
	}
	return out
}
