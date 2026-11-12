package surveyor

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
	"github.com/BeppeTemp/cartographer/internal/surveyor/recipe"
	"gopkg.in/yaml.v3"
)

// The surveyor side of a plugin: recipes/<name>/. The loader judges one folder at a
// time and sees no manifest, so what crosses the tree and the manifest (the env
// mapping) is judged by addPluginPoints once the pinned plugin is open.
func init() {
	core.Register(core.Point{Dir: "recipes", Side: core.SideSurveyor, Load: loadPluginRecipes, Check: checkPluginRecipeName, Preview: previewPluginRecipe, Executable: true})
}

type pluginRecipeFile struct {
	Shape           string   `yaml:"shape"`
	Command         string   `yaml:"command"`
	Args            []string `yaml:"args"`
	Timeout         string   `yaml:"timeout"`
	MaxOutputBytes  int      `yaml:"max_output_bytes"`
	MaxContentBytes int      `yaml:"max_content_bytes"`
	Connector       string   `yaml:"connector"`
}

func strictYAML(data []byte, into any) error {
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(into); err != nil && err != io.EOF {
		return err
	}
	return nil
}

func artifactFiles(fs []core.TreeFile, prefix string) (files []contract.ArtifactFile, exec []string) {
	for _, f := range fs {
		rel := strings.TrimPrefix(f.Path, prefix)
		files = append(files, contract.ArtifactFile{Path: rel, Content: f.Content, Executable: f.Executable})
		if f.Executable {
			exec = append(exec, rel)
		}
	}
	return files, exec
}

// treePath holds a command to a clean slash path that stays inside its folder.
func treePath(p string) bool {
	return p != "" && !strings.ContainsAny(p, `\`) && path.Clean(p) == p && !path.IsAbs(p) && p != "." && p != ".." && !strings.HasPrefix(p, "../")
}

func loadPluginRecipes(files []core.TreeFile) ([]core.Contribution, []string) {
	var problems []string
	byName := map[string][]core.TreeFile{}
	for _, f := range files {
		name, _, nested := strings.Cut(strings.TrimPrefix(f.Path, "recipes/"), "/")
		if !nested {
			problems = append(problems, f.Path+": expected a folder recipes/<name>/")
			continue
		}
		byName[name] = append(byName[name], f)
	}
	var out []core.Contribution
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		fs := byName[name]
		prefix := "recipes/" + name + "/"
		bad := func(format string, a ...any) { problems = append(problems, prefix+": "+fmt.Sprintf(format, a...)) }
		if !recipe.ValidLocal(name) {
			bad("a recipe name is 1 to 40 characters of a-z, 0-9 and -")
			continue
		}
		var instr, yml []byte
		var haveInstr, haveYML bool
		byPath := map[string]core.TreeFile{}
		for _, f := range fs {
			byPath[strings.TrimPrefix(f.Path, prefix)] = f
		}
		if f, ok := byPath["instructions.md"]; ok {
			instr, haveInstr = f.Content, true
		}
		if f, ok := byPath["recipe.yaml"]; ok {
			yml, haveYML = f.Content, true
		}
		switch {
		case !haveInstr:
			bad("instructions.md is required: the model text of the recipe")
			continue
		case len(instr) > maxInstructionsFile || !utf8.Valid(instr) || strings.TrimSpace(string(instr)) == "":
			bad("instructions.md must be non-empty UTF-8 text of at most 16 KiB")
			continue
		case !haveYML:
			bad("recipe.yaml is required")
			continue
		}
		var rf pluginRecipeFile
		if err := strictYAML(yml, &rf); err != nil {
			bad("recipe.yaml: %v", err)
			continue
		}
		spec := core.SurveyorRecipe{Shape: rf.Shape, Command: rf.Command, Args: rf.Args, Connector: rf.Connector, Instructions: string(instr),
			MaxOutput: rf.MaxOutputBytes, MaxContent: rf.MaxContentBytes}
		if msg := pluginRecipeProblem(&spec, rf, byPath); msg != "" {
			bad("recipe.yaml: %s", msg)
			continue
		}
		af, exec := artifactFiles(fs, prefix)
		out = append(out, core.Contribution{Point: "recipes", Kind: "recipe", Name: name, Files: af, Executable: exec, Recipe: &spec})
	}
	return out, problems
}

func pluginRecipeProblem(spec *core.SurveyorRecipe, rf pluginRecipeFile, in map[string]core.TreeFile) string {
	switch rf.Shape {
	case "":
		spec.Shape = recipe.ShapeFeed
	case recipe.ShapeFeed, recipe.ShapeSystem:
	default:
		return "shape must be one of feed, system"
	}
	if f, ok := in[rf.Command]; !treePath(rf.Command) || !ok {
		return "command must be a file of this recipe's folder (shipped code is a file of the tree, never a name looked up on PATH)"
	} else if !f.Executable {
		return fmt.Sprintf("command %q has no execute bit", rf.Command)
	}
	if spec.Shape == recipe.ShapeSystem && rf.Connector != "" {
		return "a system recipe has no connector: its units use KB tools only"
	}
	if rf.Connector != "" && rf.Connector != "git" && rf.Connector != "fetch" {
		return "connector must be git or fetch"
	}
	if rf.Timeout != "" {
		d, err := durationOf(rf.Timeout)
		if err != nil || d <= 0 || d > maxExecTimeout {
			return "timeout must be a duration above 0 and at most 1h"
		}
		spec.Timeout = d
	}
	if rf.MaxOutputBytes < 0 || rf.MaxOutputBytes > maxExecOutput {
		return fmt.Sprintf("max_output_bytes must be above 0 and at most %d", maxExecOutput)
	}
	if rf.MaxContentBytes < 0 || rf.MaxContentBytes > maxExecContent {
		return fmt.Sprintf("max_content_bytes must be above 0 and at most %d", maxExecContent)
	}
	return ""
}

// checkPluginRecipeName holds the qualified name a Source stores to the grammar
// (at most recipe.MaxQualifiedLen, no "-" at an edge of a part), which a recipe
// folder name alone cannot be judged against.
func checkPluginRecipeName(plugin string, c core.Contribution) []string {
	if q := recipe.Qualified(plugin, c.Name); !recipe.ValidQualified(q) {
		return []string{fmt.Sprintf("recipes/%s/: %q is not a valid qualified name (at most %d characters, no - at the edge of a part): rename the recipe or the plugin", c.Name, q, recipe.MaxQualifiedLen)}
	}
	return nil
}

func previewPluginRecipe(w io.Writer, c core.Contribution) {
	r := c.Recipe
	fmt.Fprintf(w, "  recipe %q: %s, exec %q (runs code on the Surveyor)\n", c.Name, r.Shape, r.Command)
	if r.Connector != "" {
		fmt.Fprintf(w, "    reads entries through connector %q\n", r.Connector)
	}
}
