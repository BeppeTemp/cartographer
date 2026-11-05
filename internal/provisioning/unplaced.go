package provisioning

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// ArtifactReadShape is the tool and path shape a client uses to read a KB skill
// through MCP. kb_guide's own line (internal/mcpserver/tools_guide.go) names the
// same shape; a test in each package pins that they agree.
const ArtifactReadShape = "`artifact_read` `path: skills/<name>/SKILL.md`"

// UnplacedArtifacts returns the KB skills and agents of the full manifest m
// that provider receives no file for in scope: a skill when the scope has no
// skill cell, an agent when it is neither installed as a subagent (no agent
// cell, or the client cannot run one) nor derivable into a skill (that needs a
// skill cell). Bundled artifacts are never listed: they have no MCP route.
// Call it on the manifest BEFORE ProjectManifest, which drops these; it is the
// caller that passes the result in ApplyOptions.Unplaced.
func UnplacedArtifacts(m Manifest, provider configurator.Provider, scope Scope) []Artifact {
	if destDirScoped("skill", "x", provider, scope) != "" {
		return nil
	}
	native := AgentMode(provider, scope) == AgentNative
	var out []Artifact
	for _, a := range m.Artifacts {
		if a.BuiltIn || !strings.HasPrefix(a.Source, "kb:") {
			continue
		}
		switch {
		case a.Kind == "skill":
			out = append(out, a)
		case a.Kind == "agent" && !strictAgentSkipped(a, provider) && (!native || destDirScoped("agent", a.Name, provider, scope) == ""):
			out = append(out, a)
		}
	}
	return out
}

// unplacedSentence tells a client that cannot install a KB's skills (or agents)
// as files which ones exist and how to read them through MCP (D1712). Client
// side like installedSubagentSentence: it enters no artifact hash. One paragraph
// per KB, names sorted and capped like the brief's own skills line.
func unplacedSentence(opts ApplyOptions) string {
	skills, agents := map[string][]string{}, map[string][]string{}
	var kbs []string
	for _, a := range opts.Unplaced {
		kb := strings.TrimPrefix(a.Source, "kb:")
		if skills[kb] == nil && agents[kb] == nil {
			kbs = append(kbs, kb)
		}
		if a.Kind == "skill" {
			skills[kb] = append(skills[kb], a.Name)
		} else {
			agents[kb] = append(agents[kb], a.Name)
		}
	}
	sort.Strings(kbs)
	var paras []string
	for _, kb := range kbs {
		if n := unplacedNames(skills[kb]); n != "" {
			paras = append(paras, fmt.Sprintf("The %q KB's skills are not installed as files on this client: %s. Read one with %s (pass `kb: %q` on a routed connection) and follow it; a step that needs a CLI, a script or a subagent does not apply.", kb, n, ArtifactReadShape, kb))
		}
		if n := unplacedNames(agents[kb]); n != "" {
			paras = append(paras, fmt.Sprintf("The %q KB's agents are not installed as subagents: %s. Read `agents/<name>.md` the same way and follow it yourself.", kb, n))
		}
	}
	return strings.Join(paras, "\n\n")
}

func unplacedNames(names []string) string {
	if len(names) == 0 {
		return ""
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	more := 0
	if len(sorted) > maxSkillNames {
		more, sorted = len(sorted)-maxSkillNames, sorted[:maxSkillNames]
	}
	s := strings.Join(sorted, ", ")
	if more > 0 {
		s += fmt.Sprintf(", +%d more (kb_guide)", more)
	}
	return s
}
