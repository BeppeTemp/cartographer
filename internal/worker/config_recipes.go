package worker

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/worker/recipe"
)

// rawRecipe is one recipes.<name> entry. Until the exec kind (D1262), an entry
// exists only under a built-in's name and has one key.
type rawRecipe struct {
	InstructionsFile string `yaml:"instructions_file"`
}

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
		if !builtin[name] {
			return v.errf(at, ln, "only a built-in recipe can be configured here (%s)", strings.Join(recipe.Builtins(), ", "))
		}
		f := v.raw.Recipes[name].InstructionsFile
		if f == "" {
			return v.errf(at+".instructions_file", ln, "required")
		}
		if !filepath.IsLocal(f) {
			return v.errf(at+".instructions_file", v.line("recipes", name, "instructions_file"), "must be a path inside the config file's directory")
		}
		full := filepath.Join(v.dir, f)
		b, err := os.ReadFile(full)
		switch {
		case err != nil:
			return v.errf(at+".instructions_file", v.line("recipes", name, "instructions_file"), "cannot read %s", full)
		case len(b) > maxInstructionsFile || !utf8.Valid(b) || strings.TrimSpace(string(b)) == "":
			return v.errf(at+".instructions_file", v.line("recipes", name, "instructions_file"), "%s must be non-empty UTF-8 text of at most 16 KiB", full)
		}
		if v.c.RecipeInstructions == nil {
			v.c.RecipeInstructions = map[string]string{}
		}
		v.c.RecipeInstructions[name] = string(b)
	}
	for i, h := range v.raw.Git.AllowHosts {
		if !recipe.ValidHostPattern(h) {
			return v.errf("git.allow_hosts", v.line("git", "allow_hosts"), "entry %d is not a host or *.host (no scheme, port or path)", i)
		}
	}
	v.c.GitAllowHosts = append([]string(nil), v.raw.Git.AllowHosts...)
	return nil
}
