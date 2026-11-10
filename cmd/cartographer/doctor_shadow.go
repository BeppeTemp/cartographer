package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// checkWorkspaceShadows: a KB agent or skill whose project-scope destination in
// a bound workspace is a file the repository commits. The provider then has two
// artifacts of one name and reads one of them, which one depends on the client
// (Kiro prefers the workspace's), so a name carries different mandates per
// folder. D171 refuses a collision between two KBs; this is the KB against a
// workspace.
//
// "Committed" means tracked by git: an untracked file is the user's scratch or
// Cartographer's own projection, not a shadow. It applies in every scope (the
// shadow exists with global distribution too), only to bound workspaces, and
// is silent when the candidates could not be pulled (see doctorKBCandidates).
// A provider with no project destination for a kind has nothing to shadow.
func checkWorkspaceShadows(cfg *clientconfig.Config, providers []string, cands candidateSet) []doctorFinding {
	var out []doctorFinding
	for _, provider := range providers {
		var arts []contract.Artifact
		for _, a := range cands.forProvider(cfg, provider) {
			if (a.Kind == "agent" || a.Kind == "skill") && strings.HasPrefix(a.Source, "kb:") {
				arts = append(arts, a)
			}
		}
		if len(arts) == 0 {
			continue
		}
		for _, w := range cfg.WorkspaceBindings(provider) {
			tracked, err := provisioning.TrackedPaths(w.Path)
			if err != nil || len(tracked) == 0 {
				continue
			}
			for _, a := range arts {
				// In workspace scope the folder gets only the KBs bound to it.
				if cfg.WorkspaceScope(provider) == clientconfig.ScopeWorkspace && !slices.Contains(w.KBs, strings.TrimPrefix(a.Source, "kb:")) {
					continue
				}
				dest := provisioning.ProjectDestination(a.Kind, a.Name, configurator.Provider(provider))
				if dest == "" {
					continue
				}
				file, ok := trackedUnder(tracked, dest, a.Kind)
				if !ok {
					continue
				}
				kbName := strings.TrimPrefix(a.Source, "kb:")
				src := "agents/" + a.Name + ".md"
				if a.Kind == "skill" {
					src = "skills/" + a.Name + "/SKILL.md"
				}
				out = append(out, doctorFinding{
					Check: "workspace-shadow", Severity: doctorWarning, Path: filepath.Join(w.Path, filepath.FromSlash(file)),
					Message: fmt.Sprintf("%s: %s %s from KB %s (%s) has the same name as the workspace's own %s (tracked by git): the provider reads one of them, which one depends on the client",
						provider, a.Kind, escapeControl(a.Name), escapeControl(kbName), escapeControl(src), escapeControl(file)),
					Fix: "rename one of them; a KB set artifact_prefix in its settings.yaml gives all its artifacts a scope name",
				})
			}
		}
	}
	return out
}

// trackedUnder finds the tracked file that a project destination collides with:
// the exact file for an agent, any file under the directory for a skill. For a
// skill it names the directory's SKILL.md when tracked, else the first path in
// sorted order.
func trackedUnder(tracked map[string]bool, dest, kind string) (string, bool) {
	if kind != "skill" {
		return dest, tracked[dest]
	}
	if f := dest + "/SKILL.md"; tracked[f] {
		return f, true
	}
	best := ""
	for p := range tracked {
		if strings.HasPrefix(p, dest+"/") && (best == "" || p < best) {
			best = p
		}
	}
	return best, best != ""
}
