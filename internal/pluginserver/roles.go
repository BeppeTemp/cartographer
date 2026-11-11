package pluginserver

import (
	"bytes"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
	"gopkg.in/yaml.v3"
)

func init() {
	core.Register(core.Point{Dir: "roles", Side: core.SideServer, Load: loadRoles, Preview: previewRole})
	core.Register(core.Point{Dir: "presets", Side: core.SideServer, Load: loadPresets, Preview: previewPreset})
}

// decode reads strict YAML: an unknown key is a problem.
func decode(data []byte, into any) error {
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(into); err != nil && err != io.EOF {
		return err
	}
	return nil
}

type roleFile struct {
	Description string `yaml:"description"`
	Rules       []struct {
		KB        *string  `yaml:"kb"`
		Access    string   `yaml:"access"`
		Maps      []string `yaml:"maps"`
		Journals  []string `yaml:"journals"`
		Types     []string `yaml:"types"`
		Artifacts string   `yaml:"artifacts"`
		Secrets   string   `yaml:"secrets"`
	} `yaml:"rules"`
}

// oneLine reports why a description is not one short line, or "".
func oneLine(s string) string {
	if strings.TrimSpace(s) == "" || strings.ContainsAny(s, "\n\r") || len(s) > 200 {
		return "description must be one non-empty line of at most 200 characters"
	}
	return ""
}

func loadRoles(files []core.TreeFile) ([]core.Contribution, []string) {
	var out []core.Contribution
	var problems []string
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for _, f := range files {
		stem, ok := strings.CutSuffix(strings.TrimPrefix(f.Path, "roles/"), ".yaml")
		if !ok || strings.Contains(stem, "/") || !contract.ValidArtifactName(stem) {
			problems = append(problems, f.Path+": expected a file roles/<name>.yaml")
			continue
		}
		if f.Executable {
			problems = append(problems, f.Path+": a server family holds data, not an executable file")
			continue
		}
		role, err := parseRole(f.Content)
		if err != nil {
			problems = append(problems, f.Path+": "+err.Error())
			continue
		}
		out = append(out, core.Contribution{Point: "roles", Kind: "role", Name: stem, Files: []contract.ArtifactFile{{Path: path.Base(f.Path), Content: f.Content}}, Description: role.Description, Role: &role})
	}
	return out, problems
}

// parseRole reads and judges one role file. The rules are judged by the grammar
// of auth.roles (config.ValidateAuthRoles) against a probe KB, so a role that
// loads here expands into rules the server accepts.
func parseRole(data []byte) (core.Role, error) {
	var rf roleFile
	if err := decode(data, &rf); err != nil {
		return core.Role{}, err
	}
	if msg := oneLine(rf.Description); msg != "" {
		return core.Role{}, fmt.Errorf("%s", msg)
	}
	// A selector key is declared by its presence, as in auth.roles (config.RuleSpec):
	// `maps:` and `maps: ~` decode to nothing but are declared-but-empty, which the
	// grammar refuses, never "every map".
	var nodes struct {
		Rules []yaml.Node `yaml:"rules"`
	}
	if err := yaml.Unmarshal(data, &nodes); err != nil {
		return core.Role{}, err
	}
	role := core.Role{Description: rf.Description}
	probe := config.RoleSpec{Name: "role"}
	for i, r := range rf.Rules {
		if r.KB != nil {
			return core.Role{}, fmt.Errorf("rule %d: kb is not allowed: a plugin does not know the deployment's KBs (the operator's auth.roles[].kbs names them)", i)
		}
		rule := core.RoleRule{Access: r.Access, Artifacts: r.Artifacts, Secrets: r.Secrets, Declared: map[string]bool{}}
		rule.Maps, rule.Journals, rule.Types = r.Maps, r.Journals, r.Types
		for j := 0; j+1 < len(nodes.Rules[i].Content); j += 2 {
			if k := nodes.Rules[i].Content[j].Value; k == "maps" || k == "journals" || k == "types" {
				rule.Declared[k] = true
			}
		}
		role.Rules = append(role.Rules, rule)
		probe.Rules = append(probe.Rules, config.RuleFromPlugin(rule, "probe"))
	}
	if len(role.Rules) == 0 {
		return core.Role{}, fmt.Errorf("a role needs at least one rule")
	}
	if err := config.ValidateAuthRoles(config.AuthConfig{Roles: []config.RoleSpec{probe}}); err != nil {
		return core.Role{}, err
	}
	return role, nil
}

// previewRole prints what a role can reach: its rules and the widest access.
func previewRole(w io.Writer, c core.Contribution) {
	widest, artifacts := "r", false
	for _, r := range c.Role.Rules {
		if r.Access == "rw" {
			widest = "rw"
			artifacts = artifacts || r.Artifacts != "r"
		}
	}
	fmt.Fprintf(w, "role %q: %d rule(s), widest access %s\n", c.Name, len(c.Role.Rules), widest)
	if artifacts {
		fmt.Fprintln(w, "  can change skills, agents and hooks of a KB it is mapped onto (artifacts: rw is reachable)")
	}
}
