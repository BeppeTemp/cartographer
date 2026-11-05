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
// as files which ones exist and how to read them through MCP (D1711). Client
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
		q := envQuote(kb, envNameMax)
		if len(skills[kb]) > 0 {
			paras = append(paras, fmt.Sprintf("The %s KB's skills are not installed as files on this client: %s. Read one with %s (pass `kb: %s` on a routed connection) and follow it; a step that needs a CLI, a script or a subagent does not apply.", q, unplacedNames(skills[kb]), ArtifactReadShape, q))
		}
		if len(agents[kb]) > 0 {
			paras = append(paras, fmt.Sprintf("The %s KB's agents are not installed as subagents: %s. Read `agents/<name>.md` the same way and follow it yourself.", q, unplacedNames(agents[kb])))
		}
	}
	return strings.Join(paras, "\n\n")
}

// unplacedNames sorts, quotes and bounds KB-supplied names like the other
// environment lines (envQuote, envJoin).
func unplacedNames(names []string) string {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	return envJoin(quoteAll(sorted, envNameMax))
}
