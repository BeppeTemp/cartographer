package plugin

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

func init() {
	Register(Point{Dir: "skills", Side: SideClient, Load: loadSkills, Preview: previewArtifact})
	Register(Point{Dir: "agents", Side: SideClient, Load: loadAgents, Preview: previewArtifact})
	Register(Point{Dir: "hooks", Side: SideClient, Load: loadHooks, Preview: previewArtifact, Executable: true})
	Register(Point{Dir: "providers", Side: SideClient, Load: loadProviders, Preview: previewProvider})
}

func previewArtifact(w io.Writer, c Contribution) {
	fmt.Fprintf(w, "%s %q (%d files)\n", c.Kind, c.Name, len(c.Files))
	if c.Description != "" {
		fmt.Fprintf(w, "  description: %s\n", esc(c.Description))
	}
	if c.Command != "" {
		fmt.Fprintf(w, "  command: %q on event %q\n", c.Command, c.Event)
	}
	if len(c.Requires) > 0 {
		fmt.Fprintf(w, "  requires: %q\n", c.Requires)
	}
	for _, f := range c.Executable {
		fmt.Fprintf(w, "  runs: %q\n", f)
	}
}

// group splits the files of a point into per-entry groups: skills/<name>/...
// (dirs true) or agents/<name>.md (dirs false). Paths in a group are relative to
// the entry.
func group(dir string, files []TreeFile, dirs bool) (map[string][]contract.ArtifactFile, []string) {
	out := map[string][]contract.ArtifactFile{}
	var problems []string
	for _, f := range files {
		rest := strings.TrimPrefix(f.Path, dir+"/")
		first, tail, nested := strings.Cut(rest, "/")
		switch {
		case dirs && !nested:
			problems = append(problems, fmt.Sprintf("%s: a file directly in %s/ (expected %s/<name>/...)", f.Path, dir, dir))
		case !dirs && nested:
			problems = append(problems, fmt.Sprintf("%s: expected a file %s/<name>.md", f.Path, dir))
		case !dirs && path.Ext(first) != ".md":
			problems = append(problems, fmt.Sprintf("%s: expected a file %s/<name>.md", f.Path, dir))
		case dirs:
			out[first] = append(out[first], contract.ArtifactFile{Path: tail, Content: f.Content, Executable: f.Executable})
		default:
			out[strings.TrimSuffix(first, ".md")] = append(out[strings.TrimSuffix(first, ".md")], contract.ArtifactFile{Path: first, Content: f.Content, Executable: f.Executable})
		}
	}
	return out, problems
}

// load runs check on every entry of a point, in name order.
func load(dir, kind string, files []TreeFile, dirs bool, check func(name string, fs []contract.ArtifactFile) (exec []string, problems []string)) ([]Contribution, []string) {
	groups, problems := group(dir, files, dirs)
	names := make([]string, 0, len(groups))
	for n := range groups {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []Contribution
	for _, name := range names {
		fs := groups[name]
		sort.Slice(fs, func(i, j int) bool { return fs[i].Path < fs[j].Path })
		where := fmt.Sprintf("%s %q", kind, name)
		if !provisioning.ValidArtifactName(name) {
			problems = append(problems, where+": not a valid artifact name")
			continue
		}
		// The CLI skill is left out of the default bundle listing but is bundled all the same.
		if provisioning.IsBundledArtifact(kind, name) || (kind == "skill" && strings.EqualFold(name, contract.CLISkillName)) {
			problems = append(problems, where+": a plugin cannot replace a bundled "+kind)
			continue
		}
		if err := provisioning.CheckArtifactFilePaths(fs); err != nil {
			problems = append(problems, where+": "+err.Error())
			continue
		}
		if kind == "hook" {
			// The client hashes a materialized hook with every file but hook.json
			// executable (provisioning.effectiveExecutable): the same rule here
			// makes the hash equal to the one sync computes.
			for i := range fs {
				fs[i].Executable = path.Base(fs[i].Path) != "hook.json"
			}
		}
		exec, probs := check(name, fs)
		for _, p := range probs {
			problems = append(problems, where+": "+p)
		}
		if len(probs) == 0 {
			c := Contribution{Point: dir, Kind: kind, Name: name, Files: fs, Executable: exec}
			if data, ok := find(fs, "SKILL.md"); ok && kind == "skill" {
				c.Description = descriptionOf(data)
			} else if kind == "agent" {
				c.Description = descriptionOf(fs[0].Content)
			}
			if data, ok := find(fs, "hook.json"); ok && kind == "hook" {
				var spec hookSpec
				if json.Unmarshal(data, &spec) == nil {
					c.Command, c.Event, c.Requires = spec.Command, spec.Event, spec.Requires
				}
			}
			out = append(out, c)
		}
	}
	return out, problems
}

func find(fs []contract.ArtifactFile, p string) ([]byte, bool) {
	for _, f := range fs {
		if f.Path == p {
			return f.Content, true
		}
	}
	return nil, false
}

func loadSkills(files []TreeFile) ([]Contribution, []string) {
	return load("skills", "skill", files, true, func(name string, fs []contract.ArtifactFile) ([]string, []string) {
		data, ok := find(fs, "SKILL.md")
		if !ok {
			return nil, []string{"no SKILL.md"}
		}
		if err := contract.ValidateSkillFile(name, data); err != nil {
			return nil, []string{err.Error()}
		}
		return execFiles(fs), nil
	})
}

func loadAgents(files []TreeFile) ([]Contribution, []string) {
	return load("agents", "agent", files, false, func(name string, fs []contract.ArtifactFile) ([]string, []string) {
		if err := contract.ValidateAgentFile(name, fs[0].Content); err != nil {
			return nil, []string{err.Error()}
		}
		return execFiles(fs), nil
	})
}

func loadHooks(files []TreeFile) ([]Contribution, []string) {
	return load("hooks", "hook", files, true, func(name string, fs []contract.ArtifactFile) ([]string, []string) {
		if strings.EqualFold(name, contract.BootstrapHookName) || strings.EqualFold(name, contract.WriteFindingsHookName) {
			return nil, []string{"the name is reserved"}
		}
		data, ok := find(fs, "hook.json")
		if !ok {
			return nil, []string{"no hook.json"}
		}
		var probs []string
		if _, err := contract.ValidateHookJSON(data); err != nil {
			probs = append(probs, "hook.json: "+err.Error())
		}
		if err := contract.ValidateHookRequires(name, data); err != nil {
			probs = append(probs, "hook.json: "+err.Error())
		}
		return execFiles(fs), probs
	})
}

// execFiles lists the files that carry the executable bit, sorted.
func execFiles(fs []contract.ArtifactFile) []string {
	var exec []string
	for _, f := range fs {
		if f.Executable {
			exec = append(exec, f.Path)
		}
	}
	sort.Strings(exec)
	return exec
}

// hookSpec is the part of hook.json the install preview shows.
type hookSpec struct {
	Event    string   `json:"event"`
	Command  string   `json:"command"`
	Requires []string `json:"requires"`
}

// descriptionOf reads the frontmatter description of a validated skill or agent.
func descriptionOf(data []byte) string {
	raw, _, _ := okf.SplitFrontmatter(string(data))
	fm, err := okf.ParseFrontmatter(raw)
	if err != nil {
		return ""
	}
	v, _ := fm.Get("description")
	d, _ := v.(string)
	return strings.TrimSpace(d)
}
