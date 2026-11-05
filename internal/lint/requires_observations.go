package lint

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/skill"
	"github.com/BeppeTemp/cartographer/internal/skillbundle"
)

// The two `requires` observations (D1656): suggestions for the maintainer of a
// KB's skills, agents and hooks, listed only by kb_review. Neither is a check:
// a reference to an artifact this KB does not hold may come from another KB
// bound to the same client, and a skill that names an agent in prose may not
// need it. A malformed entry is requires_invalid's (lint), not an observation's:
// entries that do not parse as <kind>:<name> are skipped here.

// artifactRequires is the `requires` list of one KB artifact.
type artifactRequires struct {
	kind, name, path string
	entries          []string
	body             string // skills only: the SKILL.md body
}

// requirableEntry splits a well-formed `<kind>:<name>` entry; a capability id
// or a malformed one answers false.
func requirableEntry(e string) (kind, name string, ok bool) {
	kind, name, ok = strings.Cut(e, ":")
	if !ok || (kind != "skill" && kind != "agent" && kind != "hook") || name == "" {
		return "", "", false
	}
	return kind, name, true
}

// kbArtifactRequires reads the `requires` of every skill, agent and hook of the
// KB, sorted by path. Unreadable or malformed lists count as empty.
func kbArtifactRequires(k *kb.KB, skills []skill.Skill) []artifactRequires {
	var out []artifactRequires
	for _, s := range skills {
		fmRaw, _, _ := okf.SplitFrontmatter(skillFileText(k, s))
		var entries []string
		if fm, err := okf.ParseFrontmatter(fmRaw); err == nil && fm != nil {
			entries, _, _ = provisioning.RequiresFromFrontmatter(fm)
		}
		out = append(out, artifactRequires{kind: "skill", name: path.Base(s.DirPath), path: skillFile(s), entries: entries, body: s.Body})
	}
	for _, name := range kbAgentNames(k) {
		rel := "agents/" + name + ".md"
		var entries []string
		if data, err := os.ReadFile(filepath.Join(k.Root, filepath.FromSlash(rel))); err == nil {
			fmRaw, _, _ := okf.SplitFrontmatter(string(data))
			if fm, err := okf.ParseFrontmatter(fmRaw); err == nil && fm != nil {
				entries, _, _ = provisioning.RequiresFromFrontmatter(fm)
			}
		}
		out = append(out, artifactRequires{kind: "agent", name: name, path: rel, entries: entries})
	}
	hooks, _ := os.ReadDir(filepath.Join(k.Root, "hooks"))
	for _, e := range hooks {
		if !e.IsDir() {
			continue
		}
		rel := "hooks/" + e.Name() + "/hook.json"
		var spec struct {
			Requires []string `json:"requires"`
		}
		if data, err := os.ReadFile(filepath.Join(k.Root, filepath.FromSlash(rel))); err == nil {
			_ = json.Unmarshal(data, &spec) // a wrong type is requires_invalid's
		}
		out = append(out, artifactRequires{kind: "hook", name: e.Name(), path: rel, entries: spec.Requires})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

func skillFileText(k *kb.KB, s skill.Skill) string {
	data, err := os.ReadFile(filepath.Join(k.Root, filepath.FromSlash(skillFile(s))))
	if err != nil {
		return ""
	}
	return string(data)
}

func kbAgentNames(k *kb.KB) []string {
	entries, err := os.ReadDir(filepath.Join(k.Root, "agents"))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	sort.Strings(names)
	return names
}

// bundledSkillNames are the skills the binary ships: a `requires` may name them.
func bundledSkillNames() map[string]bool {
	out := map[string]bool{}
	entries, err := fs.ReadDir(skillbundle.FS, "bundled")
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			out[e.Name()] = true
		}
	}
	return out
}

// requiresObservations runs both detectors over the KB's artifacts.
func requiresObservations(k *kb.KB) []Finding {
	skills, _ := loadSkills(k)
	arts := kbArtifactRequires(k, skills)
	if len(arts) == 0 {
		return nil
	}
	have := map[string]bool{}
	agents := []string{}
	for _, a := range arts {
		have[a.kind+":"+a.name] = true
		if a.kind == "agent" {
			agents = append(agents, a.name)
		}
	}
	bundle := bundledSkillNames()
	var found []Finding
	for _, a := range arts {
		var unresolved []string
		declared := map[string]bool{}
		for _, e := range a.entries {
			declared[e] = true
			kind, name, ok := requirableEntry(e)
			if !ok || have[e] || (kind == "skill" && bundle[name]) {
				continue
			}
			unresolved = append(unresolved, e)
		}
		if len(unresolved) > 0 {
			f := newObservation("requires_unresolved", Finding{
				Path:     a.path,
				Message:  fmt.Sprintf("requires %s names no artifact of this KB nor of the bundle (it may come from another KB bound to the same client)", strings.Join(unresolved, ", ")),
				Artifact: true,
			})
			found = append(found, f)
		}
		if a.kind != "skill" {
			continue
		}
		var undeclared []string
		masked := kb.MaskCodeSpans(a.body)
		for _, ag := range agents {
			if !declared["agent:"+ag] && mentionsName(masked, ag) {
				undeclared = append(undeclared, "agent:"+ag)
			}
		}
		if len(undeclared) > 0 {
			found = append(found, newObservation("requires_undeclared", Finding{
				Path:     a.path,
				Message:  fmt.Sprintf("the body names %s outside code but requires does not list it", strings.Join(undeclared, ", ")),
				Artifact: true,
			}))
		}
	}
	return found
}

// mentionsName reports a whole-word occurrence of name in text. A name is made
// of letters, digits, '-' and '_': a longer name that contains it does not count.
func mentionsName(text, name string) bool {
	re := regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])` + regexp.QuoteMeta(name) + `(?:$|[^A-Za-z0-9_-])`)
	return re.MatchString(text)
}
