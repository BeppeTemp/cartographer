package worker

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/worker/recipe"
)

// rawRecipe is one recipes.<name> entry (D1262): under a built-in's name only
// instructions_file; under any other name an exec recipe (protocol v1).
type rawRecipe struct {
	InstructionsFile string   `yaml:"instructions_file"`
	Connector        string   `yaml:"connector"`
	Shape            string   `yaml:"shape"`
	Exec             *rawExec `yaml:"exec"`
}

type rawExec struct {
	Command         string            `yaml:"command"`
	Args            []string          `yaml:"args"`
	Env             map[string]string `yaml:"env"`
	Timeout         string            `yaml:"timeout"`
	MaxOutputBytes  int               `yaml:"max_output_bytes"`
	MaxContentBytes int               `yaml:"max_content_bytes"`
}

var recipeNameRE = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// Limits of an exec recipe (docs/worker-recipes.md). max_content_bytes tops at
// the executor's own read cap, so the executor never truncates inline content.
const (
	defaultExecTimeout = 5 * time.Minute
	maxExecTimeout     = time.Hour
	defaultExecOutput  = 8 << 20
	maxExecOutput      = 64 << 20
	defaultExecContent = 128 << 10
	maxExecContent     = 1 << 20
)

type rawGit struct {
	AllowHosts []string `yaml:"allow_hosts"`
}

const maxInstructionsFile = 16 << 10

// recipes validates the recipes key and reads each instructions file now: a
// missing file is a load error with the path, not a surprise at the first poll.
// The text replaces the embedded one; it is the operator's only lever over a
// built-in, and the config is trusted (never a KB).
func (v *validator) recipes() error {
	names := make([]string, 0, len(v.raw.Recipes))
	for n := range v.raw.Recipes {
		names = append(names, n)
	}
	sort.Strings(names)
	builtin := map[string]bool{}
	for _, b := range recipe.Builtins() {
		builtin[b] = true
	}
	for _, name := range names {
		at := "recipes." + name
		ln := v.line("recipes", name)
		raw := v.raw.Recipes[name]
		if !builtin[name] {
			if err := v.execRecipe(name, raw); err != nil {
				return err
			}
			continue
		}
		if raw.Exec != nil || raw.Connector != "" || raw.Shape != "" {
			return v.errf(at, ln, "a built-in recipe takes only instructions_file; exec, connector and shape belong to a recipe of your own")
		}
		text, err := v.instructions(at, name, raw.InstructionsFile, ln)
		if err != nil {
			return err
		}
		if v.c.RecipeInstructions == nil {
			v.c.RecipeInstructions = map[string]string{}
		}
		v.c.RecipeInstructions[name] = text
	}
	for i, h := range v.raw.Git.AllowHosts {
		if !recipe.ValidHostPattern(h) {
			return v.errf("git.allow_hosts", v.line("git", "allow_hosts"), "entry %d is not a host or *.host (no scheme, port or path)", i)
		}
	}
	v.c.GitAllowHosts = append([]string(nil), v.raw.Git.AllowHosts...)
	for i, h := range v.raw.Fetch.AllowHosts {
		if !recipe.ValidHostPattern(h) {
			return v.errf("fetch.allow_hosts", v.line("fetch", "allow_hosts"), "entry %d is not a host or *.host (no scheme, port or path)", i)
		}
	}
	v.c.FetchAllowHosts = append([]string(nil), v.raw.Fetch.AllowHosts...)
	return nil
}

// instructions reads the instructions_file of recipes.<name> now: a missing file
// is a load error with the path, not a surprise at the first poll.
func (v *validator) instructions(at, name, f string, ln int) (string, error) {
	if f == "" {
		return "", v.errf(at+".instructions_file", ln, "required")
	}
	fl := v.line("recipes", name, "instructions_file")
	if !filepath.IsLocal(f) {
		return "", v.errf(at+".instructions_file", fl, "must be a path inside the config file's directory")
	}
	full := filepath.Join(v.dir, f)
	b, err := os.ReadFile(full)
	switch {
	case err != nil:
		return "", v.errf(at+".instructions_file", fl, "cannot read %s", full)
	case len(b) > maxInstructionsFile || !utf8.Valid(b) || strings.TrimSpace(string(b)) == "":
		return "", v.errf(at+".instructions_file", fl, "%s must be non-empty UTF-8 text of at most 16 KiB", full)
	}
	return string(b), nil
}

// execRecipe validates recipes.<name> of kind exec. The command comes from this
// file and from nowhere else: nothing a KB contains selects it (D1262, design D19).
// connectors() runs first, so a connector named here is already known.
func (v *validator) execRecipe(name string, r rawRecipe) error {
	at := "recipes." + name
	ln := v.line("recipes", name)
	key := func(k ...string) int {
		if l := v.line(append([]any{"recipes", name}, anySlice(k)...)...); l > 0 {
			return l
		}
		return ln
	}
	if !recipeNameRE.MatchString(name) {
		return v.errf(at, ln, "a recipe name is 1 to 40 characters of a-z, 0-9 and -")
	}
	if r.Exec == nil {
		return v.errf(at, ln, "only a built-in recipe (%s) can be configured without exec: a recipe of your own needs exec.command", strings.Join(recipe.Builtins(), ", "))
	}
	if r.Exec.Command == "" {
		return v.errf(at+".exec.command", key("exec"), "required")
	}
	switch r.Shape {
	case "", recipe.ShapeFeed, recipe.ShapeSystem:
	default:
		return v.errf(at+".shape", key("shape"), "must be one of feed, system")
	}
	if r.Shape == recipe.ShapeSystem && r.Connector != "" {
		return v.errf(at+".connector", key("connector"), "a system recipe has no connector: its units use KB tools only")
	}
	text, err := v.instructions(at, name, r.InstructionsFile, ln)
	if err != nil {
		return err
	}
	if r.Connector != "" {
		known := r.Connector == "git" || r.Connector == "fetch"
		for _, c := range v.c.Connectors {
			known = known || c.Name == r.Connector
		}
		if !known {
			return v.errf(at+".connector", key("connector"), "%q is not a connector of this config (git, fetch or one under connectors)", r.Connector)
		}
	}
	x := r.Exec
	c := recipe.ExecConfig{Name: name, Shape: r.Shape, Command: x.Command, Args: x.Args, Env: x.Env, Connector: r.Connector,
		Instructions: text, Timeout: defaultExecTimeout, MaxOutput: defaultExecOutput, MaxContent: defaultExecContent}
	if strings.ContainsAny(x.Command, `/\`) {
		// Made absolute before the child's working directory is set to a temp dir.
		if !filepath.IsAbs(x.Command) {
			c.Command = filepath.Join(v.dir, x.Command)
		}
		abs, err := filepath.Abs(c.Command)
		if err != nil {
			return v.errf(at+".exec.command", key("exec", "command"), "cannot be made absolute")
		}
		c.Command = abs
	}
	if err := envNames(x.Env); err != nil {
		return v.errf(at+".exec.env", key("exec", "env"), "%v", err)
	}
	if x.Timeout != "" {
		d, err := durationOf(x.Timeout)
		if err != nil || d <= 0 || d > maxExecTimeout {
			return v.errf(at+".exec.timeout", key("exec", "timeout"), "must be a duration above 0 and at most 1h")
		}
		c.Timeout = d
	}
	for _, f := range []struct {
		key      string
		have     int
		def, max int
		dst      *int
	}{{"max_output_bytes", x.MaxOutputBytes, defaultExecOutput, maxExecOutput, &c.MaxOutput},
		{"max_content_bytes", x.MaxContentBytes, defaultExecContent, maxExecContent, &c.MaxContent}} {
		if f.have < 0 || f.have > f.max {
			return v.errf(at+".exec."+f.key, key("exec", f.key), "must be above 0 and at most %d", f.max)
		}
		if f.have > 0 {
			*f.dst = f.have
		}
	}
	v.c.ExecRecipes = append(v.c.ExecRecipes, c)
	return nil
}

func anySlice(s []string) []any {
	out := make([]any, len(s))
	for i, x := range s {
		out[i] = x
	}
	return out
}
