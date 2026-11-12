package surveyor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore/recipes"
)

const (
	okRecipeYAML = "command: run.sh\n"
	okConnYAML   = "transport: stdio\ncommand: bin/mcp\nenv: [WIKI_TOKEN]\ntools: [read]\n"
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
	cs, probs := loadPluginRecipes(recipeTree("feed", okRecipeYAML+"timeout: 2m\nconnector: wiki\n"))
	if len(probs) != 0 || len(cs) != 1 {
		t.Fatalf("%v %v", cs, probs)
	}
	r := cs[0].Recipe
	if r.Shape != "feed" || r.Command != "run.sh" || r.Timeout.Minutes() != 2 || r.Connector != "wiki" || len(cs[0].Executable) != 1 || cs[0].Executable[0] != "run.sh" {
		t.Errorf("%+v %v", r, cs[0].Executable)
	}
	noInstr := recipeTree("feed", okRecipeYAML)
	noYAML := recipeTree("feed", okRecipeYAML)
	for name, tc := range map[string]struct {
		files []core.TreeFile
		want  string
	}{
		"no instructions": {append(noInstr[1:2:2], noInstr[2]), "instructions.md is required"},
		"no recipe.yaml":  {[]core.TreeFile{noYAML[0], noYAML[2]}, "instructions.md alone"},
		"flat file":       {[]core.TreeFile{tf("recipes/x.yaml", "", false)}, "expected a folder"},
		"bad name":        {recipeTree("Feed", okRecipeYAML), "1 to 40 characters"},
		"unknown key":     {recipeTree("feed", okRecipeYAML+"colour: red\n"), "colour not found"},
		"outside folder":  {recipeTree("feed", "command: ../other/run.sh\n"), "file of this recipe's folder"},
		"bare name":       {recipeTree("feed", "command: sh\n"), "file of this recipe's folder"},
		"not executable":  {recipeTree("feed", "command: recipe.yaml\n"), "no execute bit"},
		"system+conn":     {recipeTree("feed", okRecipeYAML+"shape: system\nconnector: git\n"), "no connector"},
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

func TestLoadPluginConnectors(t *testing.T) {
	files := []core.TreeFile{tf("connectors/wiki.yaml", okConnYAML, false), tf("connectors/bin/mcp", "x", true),
		tf("connectors/web.yaml", "transport: http\nurl: https://wiki.example.com/mcp\ntoken_env: WIKI_TOKEN\nheaders: {X-Org: ORG_NAME}\ntools: [read]\n", false),
		tf("connectors/path.yaml", "transport: stdio\ncommand: wiki-mcp\ntools: [read]\n", false)}
	cs, probs := loadPluginConnectors(files)
	if len(probs) != 0 || len(cs) != 3 {
		t.Fatalf("%v %v", cs, probs)
	}
	by := map[string]core.Contribution{}
	for _, c := range cs {
		by[c.Name] = c
	}
	if k := by["wiki"]; k.Connector.CommandOnPath || len(k.Executable) != 1 || k.Connector.Command != "bin/mcp" {
		t.Errorf("tree command: %+v", k)
	}
	if k := by["path"]; !k.Connector.CommandOnPath || len(k.Executable) != 0 {
		t.Errorf("PATH command is flagged, not an executable of the tree: %+v", k)
	}
	for name, tc := range map[string]struct {
		files []core.TreeFile
		want  string
	}{
		"no tools":       {[]core.TreeFile{tf("connectors/a.yaml", "transport: http\nurl: https://wiki.example.com/mcp\n", false)}, "tools is required"},
		"http url":       {[]core.TreeFile{tf("connectors/a.yaml", "transport: http\nurl: http://wiki.example.com/mcp\ntools: [r]\n", false)}, "url"},
		"http loopback":  {[]core.TreeFile{tf("connectors/a.yaml", "transport: http\nurl: http://127.0.0.1:8080/mcp\ntools: [r]\n", false)}, "url must be https"},
		"url creds":      {[]core.TreeFile{tf("connectors/a.yaml", "transport: http\nurl: https://u:p@wiki.example.com/mcp\ntools: [r]\n", false)}, "credentials"},
		"literal secret": {[]core.TreeFile{tf("connectors/a.yaml", "transport: http\nurl: https://wiki.example.com/mcp\ntoken_env: sk-abc\ntools: [r]\n", false)}, "token_env"},
		"env value":      {[]core.TreeFile{tf("connectors/a.yaml", "transport: stdio\ncommand: x\nenv: [lower]\ntools: [r]\n", false)}, "variable names"},
		"missing exec":   {[]core.TreeFile{tf("connectors/a.yaml", "transport: stdio\ncommand: bin/none\ntools: [r]\n", false)}, "executable file under connectors/"},
		"stray exec":     {[]core.TreeFile{tf("connectors/bin/x", "x", true)}, "no connector names"},
		"reserved":       {[]core.TreeFile{tf("connectors/git.yaml", okConnYAML, false)}, "not git"},
		"flat helper":    {[]core.TreeFile{tf("connectors/run.sh", "x", true)}, "expected a file connectors/<name>.yaml"},
		"mixed":          {[]core.TreeFile{tf("connectors/a.yaml", "transport: stdio\ncommand: x\nurl: https://x.example.com\ntools: [r]\n", false)}, "belong to transport http"},
	} {
		t.Run(name, func(t *testing.T) {
			_, probs := loadPluginConnectors(tc.files)
			if len(probs) == 0 || !strings.Contains(strings.Join(probs, "\n"), tc.want) {
				t.Fatalf("problems %v, want %q", probs, tc.want)
			}
		})
	}
}

const pointsManifest = "schema: 2\nname: acme-wiki\nversion: 1.0.0\ndisplay_name: Wiki\ndescription: A wiki reader.\ncartographer: \">=0.29.0\"\ncomponents:\n  surveyor:\n    sdk: {recipes: 1, connectors: 1}\n    permissions:\n      net: [wiki.example.com]\n      env: [WIKI_TOKEN]\n"

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
	tree := append(recipeTree("feed", okRecipeYAML+"connector: wiki\n"),
		tf("connectors/wiki.yaml", okConnYAML, false), tf("connectors/bin/mcp", "x", true),
		tf("connectors/web.yaml", "transport: http\nurl: https://wiki.example.com/mcp\ntoken_env: WIKI_TOKEN\ntools: [read]\n", false))
	p := pointsPlugin(t, pointsManifest, tree...)
	c := pointsConfig(t, p, map[string]string{"WIKI_TOKEN": "OPERATOR_WIKI_TOKEN"})
	if err := c.addPluginPoints(); err != nil {
		t.Fatal(err)
	}
	if len(c.ExecRecipes) != 1 || len(c.Connectors) != 2 {
		t.Fatalf("%+v %+v", c.ExecRecipes, c.Connectors)
	}
	r := c.ExecRecipes[0]
	root := filepath.Join(c.PluginsDir, "acme-wiki", p.Digest)
	if r.Name != "acme-wiki/feed" || r.Connector != "acme-wiki/wiki" || r.Command != filepath.Join(root, "recipes", "feed", "run.sh") || r.Env["WIKI_TOKEN"] != "OPERATOR_WIKI_TOKEN" || r.Shape != "feed" {
		t.Errorf("recipe %+v", r)
	}
	web, wiki := c.Connectors[1], c.Connectors[0]
	if wiki.Name == "acme-wiki/web" {
		web, wiki = wiki, web
	}
	if wiki.Name != "acme-wiki/wiki" || wiki.Command != filepath.Join(root, "connectors", "bin", "mcp") || wiki.Env["WIKI_TOKEN"] != "OPERATOR_WIKI_TOKEN" {
		t.Errorf("stdio %+v", wiki)
	}
	if web.TokenEnv != "OPERATOR_WIKI_TOKEN" || web.URL != "https://wiki.example.com/mcp" {
		t.Errorf("http %+v", web)
	}
}

func TestAddPluginPointsRefusals(t *testing.T) {
	mapped := map[string]string{"WIKI_TOKEN": "A"}
	otherEnv := strings.Replace(pointsManifest, "env: [WIKI_TOKEN]", "env: [OTHER]", 1)
	for name, tc := range map[string]struct {
		manifest string
		tree     []core.TreeFile
		env      map[string]string
		want     string
	}{
		"unmapped":       {pointsManifest, recipeTree("feed", okRecipeYAML), nil, "declares the variable WIKI_TOKEN"},
		"extra":          {pointsManifest, recipeTree("feed", okRecipeYAML), map[string]string{"WIKI_TOKEN": "A", "OTHER": "B"}, "env maps OTHER"},
		"unknown conn":   {pointsManifest, recipeTree("feed", okRecipeYAML+"connector: nope\n"), mapped, `connector "nope" is not a connector of this plugin`},
		"undeclared env": {otherEnv, []core.TreeFile{tf("connectors/a.yaml", okConnYAML, false), tf("connectors/bin/mcp", "x", true)}, map[string]string{"OTHER": "A"}, "does not declare"},
		"host not in net": {pointsManifest, []core.TreeFile{tf("connectors/a.yaml", "transport: http\nurl: https://other.example.com/mcp\ntools: [r]\n", false)}, mapped,
			"other.example.com is not under permissions.net"},
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
	tree := append(recipeTree("feed", okRecipeYAML), tf("connectors/wiki.yaml", okConnYAML, false), tf("connectors/bin/mcp", "x", true),
		tf("connectors/path.yaml", "transport: stdio\ncommand: wiki-mcp\nargs: [-c, serve]\ntools: [read]\n", false))
	p := pointsPlugin(t, pointsManifest, tree...)
	var b strings.Builder
	for _, pt := range core.RegisteredPoints() {
		for _, c := range p.Contributions[pt.Dir] {
			pt.Preview(&b, c)
		}
	}
	for _, want := range []string{`recipe "feed": feed, exec "run.sh" (runs code on the Surveyor)`, `connector "wiki": stdio, runs "bin/mcp"`, `runs program "wiki-mcp" looked up on the Surveyor's PATH with args ["-c" "serve"] (runs code on the Surveyor)`, "the model may use 1 tool(s): read"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("preview lacks %q:\n%s", want, b.String())
		}
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

// A Starlark recipe of a pinned plugin is read by the tree reader (the script
// check), loaded by the config and polled end to end under its qualified name;
// its env.get sees only the mapped variable.
func TestStarlarkPluginRecipePollsEndToEnd(t *testing.T) {
	manifest := pointsManifest
	script := `
def lst(req):
    t = env.get("WIKI_TOKEN")
    return {"entries": [{"key": "p1", "title": "Page", "revision": "1", "change": "added", "content": "token length " + str(len(t))}], "next_cursor": "c1"}

recipe("feed", lst)
`
	files := []core.TreeFile{tf(core.ManifestFile, manifest, false), tf("recipes/feed/instructions.md", "Read the entries.", false), tf(core.SurveyorScript, script, false)}
	opts := core.Options{RunningVersion: "0.29.0", CheckScripts: recipes.Check}
	p, probs := core.ReadTree(files, opts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	// A plugin with no script and no recipe folder reads clean.
	if _, probs := core.ReadTree(files[:1:1], opts); len(probs) != 0 {
		t.Fatalf("a plugin without the script: %v", probs)
	}
	bad := []core.TreeFile{files[0], files[2]}
	if _, probs := core.ReadTree(bad, opts); !strings.Contains(strings.Join(probs, "\n"), "needs recipes/feed/instructions.md") {
		t.Fatalf("problems %v", probs)
	}
	store := t.TempDir()
	if err := (core.Store{Root: store}).Put("acme-wiki", files, p.Digest, opts); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPERATOR_WIKI", "abcd")
	c, err := cfgLoad(t, cfgGolden+"plugins_dir: "+store+"\nplugins:\n  acme-wiki:\n    digest: "+p.Digest+"\n    env: {WIKI_TOKEN: OPERATOR_WIKI}\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.LoadPlugins(opts); err != nil {
		t.Fatal(err)
	}
	if len(c.StarRecipes) != 1 || c.StarRecipes[0].Name != "acme-wiki/feed" || len(c.ExecRecipes) != 0 {
		t.Fatalf("%+v", c.StarRecipes)
	}
	e := newPollEnv(t, Config{})
	e.in = e.instance(Config{GitAllowHosts: []string{"git.example.com"}, StarRecipes: c.StarRecipes})
	e.feed()["recipe"], e.feed()["locator"] = "acme-wiki/feed", "wiki:space/ops"
	delete(e.feed(), "feed_include")
	if _, _, err := e.poll(); err != nil {
		t.Fatal(err)
	}
	if fe, _ := e.feed()["feed_error"].(string); fe != "" || len(e.k.units()) == 0 {
		t.Fatalf("feed_error %q, units %v", fe, e.k.units())
	}
}

func TestStarlarkRecipeGuards(t *testing.T) {
	script := "def l(r):\n    return {}\nrecipe(\"feed\", l)\n"
	// Read without the script check (a tree read by a binary that links none), the
	// config still refuses a registration with no folder.
	p := pointsPlugin(t, pointsManifest, tf(core.SurveyorScript, script, false))
	c := pointsConfig(t, p, map[string]string{"WIKI_TOKEN": "A"})
	if err := c.addPluginPoints(); err == nil || !strings.Contains(err.Error(), "registers recipe feed") {
		t.Errorf("registration without a folder: %v", err)
	}
	// A folder with instructions.md alone and no registration.
	p = pointsPlugin(t, pointsManifest, tf("recipes/feed/instructions.md", "x", false))
	c = pointsConfig(t, p, map[string]string{"WIKI_TOKEN": "A"})
	if err := c.addPluginPoints(); err == nil || !strings.Contains(err.Error(), "does not register it") {
		t.Errorf("folder without a registration: %v", err)
	}
	// The preview names the Starlark recipe.
	var b strings.Builder
	for _, c := range p.Contributions["recipes"] {
		previewPluginRecipe(&b, c)
	}
	if !strings.Contains(b.String(), "Starlark, registered by surveyor.star") {
		t.Errorf("preview %q", b.String())
	}
}
