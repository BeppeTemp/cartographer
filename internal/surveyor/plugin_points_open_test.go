package surveyor

import (
	"path/filepath"
	"testing"
)

// PluginPoints builds a plugin the way a pinned one is built, with its files
// read from the folder given: what `plugin test` runs is what a Surveyor runs.
func TestPluginPointsReadsTheFolderItIsGiven(t *testing.T) {
	p := pointsPlugin(t, pointsManifest, recipeTree("feed", okRecipeYAML)...)
	root := filepath.Join(t.TempDir(), "my-plugin")
	c, err := PluginPoints(p, PluginPin{Enabled: true, Env: map[string]string{"WIKI_TOKEN": "TEST_VAR"}}, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.ExecRecipes) != 1 {
		t.Fatalf("%+v", c.ExecRecipes)
	}
	if r := c.ExecRecipes[0]; r.Name != "acme-wiki/feed" || r.Command != filepath.Join(root, "recipes", "feed", "run.sh") || r.Env["WIKI_TOKEN"] != "TEST_VAR" {
		t.Errorf("%+v", r)
	}
	// The same refusals as at start: a declared variable left unmapped.
	if _, err := PluginPoints(p, PluginPin{Enabled: true}, root); err == nil {
		t.Error("an unmapped variable was accepted")
	}
}
