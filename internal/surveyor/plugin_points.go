package surveyor

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
	"github.com/BeppeTemp/cartographer/internal/surveyor/connector"
	"github.com/BeppeTemp/cartographer/internal/surveyor/recipe"
	"gopkg.in/yaml.v3"
)

// The surveyor side of a plugin: recipes/<name>/ and connectors/<name>.yaml.
// These loaders judge one folder at a time and see no manifest, so everything
// that crosses the tree and the manifest (env names against permissions.env, a
// URL host against net, a recipe's connector against the plugin's connectors)
// is judged by addPluginPoints once the pinned plugin is open.
func init() {
	core.Register(core.Point{Dir: "recipes", Side: core.SideSurveyor, Load: loadPluginRecipes, Check: checkPluginRecipeName, Preview: previewPluginRecipe, Executable: true})
	core.Register(core.Point{Dir: "connectors", Side: core.SideSurveyor, Load: loadPluginConnectors, Preview: previewPluginConnector, Executable: true})
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

type pluginConnectorFile struct {
	Transport      string            `yaml:"transport"`
	Command        string            `yaml:"command"`
	Args           []string          `yaml:"args"`
	Env            []string          `yaml:"env"`
	URL            string            `yaml:"url"`
	TokenEnv       string            `yaml:"token_env"`
	Headers        map[string]string `yaml:"headers"`
	Tools          []string          `yaml:"tools"`
	CallTimeout    string            `yaml:"call_timeout"`
	MaxResultBytes int               `yaml:"max_result_bytes"`
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
			// A recipe without recipe.yaml is a Starlark one: surveyor.star registers
			// it (core.ReadTree's script check judges that) and nothing in its
			// folder runs, so a second file is a mistake.
			if len(fs) != 1 {
				bad("a recipe without recipe.yaml is a Starlark recipe and its folder holds instructions.md alone")
				continue
			}
			out = append(out, core.Contribution{Point: "recipes", Kind: "recipe", Name: name,
				Files: []contract.ArtifactFile{{Path: "instructions.md", Content: instr}}, Recipe: &core.SurveyorRecipe{Script: true, Instructions: string(instr)}})
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
	if rf.Connector != "" && rf.Connector != "git" && rf.Connector != "fetch" && !connectorNameRE.MatchString(rf.Connector) {
		return "connector must be git, fetch or a connector of this plugin"
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

func loadPluginConnectors(files []core.TreeFile) ([]core.Contribution, []string) {
	var problems []string
	var out []core.Contribution
	execFiles := map[string]core.TreeFile{} // executable helper files, path relative to connectors/
	var yamls []core.TreeFile
	for _, f := range files {
		rel := strings.TrimPrefix(f.Path, "connectors/")
		switch {
		case strings.Contains(rel, "/"):
			if f.Executable {
				execFiles[rel] = f
			}
		case strings.HasSuffix(rel, ".yaml"):
			yamls = append(yamls, f)
		default:
			problems = append(problems, f.Path+": expected a file connectors/<name>.yaml (helper files go in a subfolder)")
		}
	}
	sort.Slice(yamls, func(i, j int) bool { return yamls[i].Path < yamls[j].Path })
	used := map[string]bool{}
	for _, f := range yamls {
		name := strings.TrimSuffix(strings.TrimPrefix(f.Path, "connectors/"), ".yaml")
		bad := func(format string, a ...any) { problems = append(problems, f.Path+": "+fmt.Sprintf(format, a...)) }
		if !connectorNameRE.MatchString(name) || slices.Contains(reservedConnectors, name) {
			bad("a connector name is 1 to 30 characters of a-z, 0-9 and -, and not git, fetch or artifact")
			continue
		}
		var cf pluginConnectorFile
		if err := strictYAML(f.Content, &cf); err != nil {
			bad("%v", err)
			continue
		}
		spec := core.SurveyorConnector{Transport: cf.Transport, Command: cf.Command, Args: cf.Args, Env: cf.Env, URL: cf.URL, TokenEnv: cf.TokenEnv,
			Headers: cf.Headers, Tools: cf.Tools, MaxResult: cf.MaxResultBytes}
		if msg := pluginConnectorProblem(&spec, cf, execFiles); msg != "" {
			bad("%s", msg)
			continue
		}
		var exec []string
		if spec.Command != "" && !spec.CommandOnPath {
			used[spec.Command] = true
			exec = []string{spec.Command}
		}
		out = append(out, core.Contribution{Point: "connectors", Kind: "connector", Name: name,
			Files: []contract.ArtifactFile{{Path: path.Base(f.Path), Content: f.Content}}, Executable: exec, Connector: &spec})
	}
	// Every executable file must be a connector's command, or the preview
	// ("runs code on the Surveyor") would not list it.
	for _, p := range slices.Sorted(maps.Keys(execFiles)) {
		if !used[p] {
			problems = append(problems, "connectors/"+p+": an executable file that no connector names as its command")
		}
	}
	return out, problems
}

func pluginConnectorProblem(spec *core.SurveyorConnector, cf pluginConnectorFile, execFiles map[string]core.TreeFile) string {
	switch cf.Transport {
	case connector.TransportStdio:
		switch {
		case cf.Command == "":
			return "command is required for transport stdio"
		case cf.URL != "" || cf.TokenEnv != "" || len(cf.Headers) > 0:
			return "url, token_env and headers belong to transport http"
		case !strings.Contains(cf.Command, "/"):
			spec.CommandOnPath = !strings.ContainsAny(cf.Command, `\`)
			if !spec.CommandOnPath {
				return "command must be a slash path of a file under connectors/ or a bare program name"
			}
		case !treePath(cf.Command) || execFiles[cf.Command].Path == "":
			return fmt.Sprintf("command %q must be an executable file under connectors/", cf.Command)
		}
		if msg := envListProblem(cf.Env); msg != "" {
			return msg
		}
	case connector.TransportHTTP:
		switch {
		case cf.URL == "":
			return "url is required for transport http"
		case cf.Command != "" || len(cf.Args) > 0 || len(cf.Env) > 0:
			return "command, args and env belong to transport stdio"
		}
		if !strings.HasPrefix(strings.ToLower(cf.URL), "https://") {
			return "url must be https (plain http to a loopback or private host needs the private-network grant, a later change)"
		}
		if err := checkURL(cf.URL, false); err != nil {
			return "url " + err.Error()
		}
		if cf.TokenEnv != "" && !envNameRE.MatchString(cf.TokenEnv) {
			return "token_env must be a variable name (capitals, digits and _)"
		}
		for h, n := range cf.Headers {
			if strings.TrimSpace(h) == "" || !envNameRE.MatchString(n) {
				return "headers map a header to a variable name (capitals, digits and _), never a value"
			}
		}
	default:
		return "transport is required: stdio or http"
	}
	if len(cf.Tools) == 0 {
		return "tools is required: the tools the model may use"
	}
	if msg := toolsProblem(cf.Tools); msg != "" {
		return "tools: " + msg
	}
	if cf.CallTimeout != "" {
		d, err := durationOf(cf.CallTimeout)
		if err != nil || d <= 0 {
			return "call_timeout must be a positive duration such as 60s"
		}
		spec.CallTimeout = d
	}
	if cf.MaxResultBytes < 0 {
		return "max_result_bytes must be above 0"
	}
	return ""
}

func envListProblem(names []string) string {
	seen := map[string]bool{}
	for _, n := range names {
		if !envNameRE.MatchString(n) || seen[n] {
			return "env lists variable names (capitals, digits and _), each once, never values"
		}
		seen[n] = true
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
	if r.Script {
		fmt.Fprintf(w, "  recipe %q: Starlark, registered by surveyor.star (runs on the Surveyor, with the http, env and log modules, no process)\n", c.Name)
		return
	}
	fmt.Fprintf(w, "  recipe %q: %s, exec %q (runs code on the Surveyor)\n", c.Name, r.Shape, r.Command)
	if r.Connector != "" {
		fmt.Fprintf(w, "    reads entries through connector %q\n", r.Connector)
	}
}

func previewPluginConnector(w io.Writer, c core.Contribution) {
	k := c.Connector
	switch {
	case k.Transport == connector.TransportHTTP:
		fmt.Fprintf(w, "  connector %q: http %q\n", c.Name, k.URL)
	case k.CommandOnPath:
		fmt.Fprintf(w, "  connector %q: stdio, runs program %q looked up on the Surveyor's PATH with args %q (runs code on the Surveyor)\n", c.Name, k.Command, k.Args)
	default:
		fmt.Fprintf(w, "  connector %q: stdio, runs %q (runs code on the Surveyor)\n", c.Name, k.Command)
	}
	fmt.Fprintf(w, "    the model may use %d tool(s): %s\n", len(k.Tools), strings.Join(k.Tools, ", "))
}
