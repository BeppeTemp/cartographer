package surveyor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

const (
	okRecipeYAML = "command: run.sh\n"
)

func tf(path, content string, exec bool) core.TreeFile {
	return core.TreeFile{Path: path, Content: []byte(content), Executable: exec}
}

func recipeTree(name, yaml string, extra ...core.TreeFile) []core.TreeFile {
	return append([]core.TreeFile{
		tf("recipes/"+name+"/instructions.md", "Read the entries.", false),
		tf("recipes/"+name+"/recipe.yaml", yaml, false),
		tf("recipes/"+name+"/run.sh", "#!/bin/sh\n", true),
	}, extra...)
}

func TestLoadPluginRecipes(t *testing.T) {
	cs, probs := loadPluginRecipes(recipeTree("feed", okRecipeYAML+"timeout: 2m\nconnector: git\n"))
	if len(probs) != 0 || len(cs) != 1 {
		t.Fatalf("%v %v", cs, probs)
	}
	r := cs[0].Recipe
	if r.Shape != "feed" || r.Command != "run.sh" || r.Timeout.Minutes() != 2 || r.Connector != "git" || len(cs[0].Executable) != 1 || cs[0].Executable[0] != "run.sh" {
		t.Errorf("%+v %v", r, cs[0].Executable)
	}
	noInstr := recipeTree("feed", okRecipeYAML)
	noYAML := recipeTree("feed", okRecipeYAML)
	for name, tc := range map[string]struct {
		files []core.TreeFile
		want  string
	}{
		"no instructions": {append(noInstr[1:2:2], noInstr[2]), "instructions.md is required"},
		"no recipe.yaml":  {[]core.TreeFile{noYAML[0], noYAML[2]}, "recipe.yaml is required"},
		"flat file":       {[]core.TreeFile{tf("recipes/x.yaml", "", false)}, "expected a folder"},
		"bad name":        {recipeTree("Feed", okRecipeYAML), "1 to 40 characters"},
		"unknown key":     {recipeTree("feed", okRecipeYAML+"colour: red\n"), "colour not found"},
		"outside folder":  {recipeTree("feed", "command: ../other/run.sh\n"), "file of this recipe's folder"},
		"bare name":       {recipeTree("feed", "command: sh\n"), "file of this recipe's folder"},
		"not executable":  {recipeTree("feed", "command: recipe.yaml\n"), "no execute bit"},
		"system+conn":     {recipeTree("feed", okRecipeYAML+"shape: system\nconnector: git\n"), "no connector"},
		"other connector": {recipeTree("feed", okRecipeYAML+"connector: wiki\n"), "connector must be git or fetch"},
		"bad shape":       {recipeTree("feed", okRecipeYAML+"shape: lake\n"), "shape must be"},
		"big timeout":     {recipeTree("feed", okRecipeYAML+"timeout: 3h\n"), "timeout must be"},
		"big output":      {recipeTree("feed", okRecipeYAML+"max_output_bytes: 999999999\n"), "max_output_bytes"},
	} {
		t.Run(name, func(t *testing.T) {
			_, probs := loadPluginRecipes(tc.files)
			if len(probs) == 0 || !strings.Contains(strings.Join(probs, "\n"), tc.want) {
				t.Fatalf("problems %v, want %q", probs, tc.want)
			}
		})
	}
}

const pointsManifest = "schema: 2\nname: acme-wiki\nversion: 1.0.0\ndisplay_name: Wiki\ndescription: A wiki reader.\ncartographer: \">=0.29.0\"\ncomponents:\n  surveyor:\n    sdk: {recipes: 1}\n    permissions:\n      net: [wiki.example.com]\n      env: [WIKI_TOKEN]\n"

func pointsPlugin(t *testing.T, manifest string, extra ...core.TreeFile) *core.Plugin {
	t.Helper()
	files := append([]core.TreeFile{tf(core.ManifestFile, manifest, false)}, extra...)
	p, probs := core.ReadTree(files, core.Options{RunningVersion: "0.29.0", Side: core.SideSurveyor})
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	return &p
}

func pointsConfig(t *testing.T, p *core.Plugin, env map[string]string) *Config {
	name := p.Manifest.Name
	return &Config{PluginsDir: filepath.Join(t.TempDir(), "store"), Plugins: []*core.Plugin{p},
		PluginPins: map[string]PluginPin{name: {Enabled: true, Digest: p.Digest, Env: env, Line: 7}}}
}

func TestAddPluginPoints(t *testing.T) {
	p := pointsPlugin(t, pointsManifest, recipeTree("feed", okRecipeYAML+"connector: git\n")...)
	c := pointsConfig(t, p, map[string]string{"WIKI_TOKEN": "OPERATOR_WIKI_TOKEN"})
	if err := c.addPluginPoints(); err != nil {
		t.Fatal(err)
	}
	if len(c.ExecRecipes) != 1 {
		t.Fatalf("%+v", c.ExecRecipes)
	}
	r := c.ExecRecipes[0]
	root := filepath.Join(c.PluginsDir, "acme-wiki", p.Digest)
	if r.Name != "acme-wiki/feed" || r.Connector != "git" || r.Command != filepath.Join(root, "recipes", "feed", "run.sh") || r.Env["WIKI_TOKEN"] != "OPERATOR_WIKI_TOKEN" || r.Shape != "feed" {
		t.Errorf("recipe %+v", r)
	}
}

func TestAddPluginPointsRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		manifest string
		tree     []core.TreeFile
		env      map[string]string
		want     string
	}{
		"unmapped": {pointsManifest, recipeTree("feed", okRecipeYAML), nil, "declares the variable WIKI_TOKEN"},
		"extra":    {pointsManifest, recipeTree("feed", okRecipeYAML), map[string]string{"WIKI_TOKEN": "A", "OTHER": "B"}, "env maps OTHER"},
	} {
		t.Run(name, func(t *testing.T) {
			c := pointsConfig(t, pointsPlugin(t, tc.manifest, tc.tree...), tc.env)
			err := c.addPluginPoints()
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "(line 7)") {
				t.Fatalf("err = %v, want %q with the line", err, tc.want)
			}
		})
	}
}

// A plugin's recipe runs through the same poll as an operator's, end to end:
// the pinned tree is vendored, the config maps the credential, a Source names
// "<plugin>/<recipe>" and the script (here a shell wrapper over the test binary)
// sees only the mapped variable.
func TestPluginRecipePollsEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the recipe is a shell script")
	}
	manifest := strings.Replace(pointsManifest, "env: [WIKI_TOKEN]", "env: [HELPER_BIN]", 1)
	script := "#!/bin/sh\nexec \"$HELPER_BIN\" " + execHelperFlag + " fetch " + os.DevNull + "\n"
	files := []core.TreeFile{tf(core.ManifestFile, manifest, false), tf("recipes/feed/instructions.md", "Read the entries.", false),
		tf("recipes/feed/recipe.yaml", okRecipeYAML+"timeout: 20s\n", false), tf("recipes/feed/run.sh", script, true)}
	opts := core.Options{RunningVersion: "0.29.0"}
	p, probs := core.ReadTree(files, opts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	store := t.TempDir()
	if err := (core.Store{Root: store}).Put("acme-wiki", files, p.Digest, opts); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPERATOR_HELPER", os.Args[0])
	c, err := cfgLoad(t, cfgGolden+"plugins_dir: "+store+"\nplugins:\n  acme-wiki:\n    digest: "+p.Digest+"\n    env: {HELPER_BIN: OPERATOR_HELPER}\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.LoadPlugins(opts); err != nil {
		t.Fatal(err)
	}
	if len(c.ExecRecipes) != 1 || c.ExecRecipes[0].Name != "acme-wiki/feed" {
		t.Fatalf("%+v", c.ExecRecipes)
	}
	e := newPollEnv(t, Config{})
	e.in = e.instance(Config{GitAllowHosts: []string{"git.example.com"}, ExecRecipes: c.ExecRecipes})
	e.feed()["recipe"], e.feed()["locator"] = "acme-wiki/feed", "wiki:space/ops"
	delete(e.feed(), "feed_include")
	if _, _, err := e.poll(); err != nil {
		t.Fatal(err)
	}
	if fe, _ := e.feed()["feed_error"].(string); fe != "" || len(e.k.units()) == 0 {
		t.Fatalf("feed_error %q, units %v", fe, e.k.units())
	}
}

func TestPluginEnvKeyIsValidated(t *testing.T) {
	_, err := cfgLoad(t, cfgGolden+"plugins_dir: "+t.TempDir()+"\nplugins:\n  feeds: {digest: "+pluginDigest+", env: {TOKEN: sk-literal-value}}\n")
	if err == nil || !strings.Contains(err.Error(), "plugins.feeds.env") || strings.Contains(err.Error(), "sk-literal-value") {
		t.Fatalf("a literal value is refused without echoing it: %v", err)
	}
}

// The install preview of a pinned plugin names what runs on the Surveyor.
func TestPluginPointPreviews(t *testing.T) {
	p := pointsPlugin(t, pointsManifest, recipeTree("feed", okRecipeYAML)...)
	var b strings.Builder
	for _, pt := range core.RegisteredPoints() {
		for _, c := range p.Contributions[pt.Dir] {
			pt.Preview(&b, c)
		}
	}
	if want := `recipe "feed": feed, exec "run.sh" (runs code on the Surveyor)`; !strings.Contains(b.String(), want) {
		t.Errorf("preview lacks %q:\n%s", want, b.String())
	}
}

// A recipe folder name that is fine alone but makes the qualified name invalid
// is a tree problem, so vendoring and the preview report it before any start.
func TestQualifiedRecipeNameIsJudgedByTheTreeReader(t *testing.T) {
	for name, tc := range map[string]struct{ plugin, recipe string }{
		"too long":   {"acme-wiki-with-a-long-name", "a-recipe-with-a-long-name"},
		"dash edge":  {"acme-wiki", "-feed"},
		"dash edge2": {"acme-wiki", "feed-"},
	} {
		t.Run(name, func(t *testing.T) {
			m := strings.Replace(pointsManifest, "name: acme-wiki", "name: "+tc.plugin, 1)
			files := append([]core.TreeFile{tf(core.ManifestFile, m, false)}, recipeTree(tc.recipe, okRecipeYAML)...)
			_, probs := core.ReadTree(files, core.Options{RunningVersion: "0.29.0"})
			if !strings.Contains(strings.Join(probs, "\n"), "not a valid qualified name") {
				t.Fatalf("problems %v", probs)
			}
		})
	}
}
