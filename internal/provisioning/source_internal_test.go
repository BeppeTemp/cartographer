package provisioning

import (
	"reflect"
	"testing"
)

// A {{path:...}} placeholder in a plugin skill is a global one: it joins the
// table with no KB, like a bundled skill's, and only while its plugin is active.
func TestPlaceholderSourcesIncludeAnActivePluginSkill(t *testing.T) {
	m := Manifest{Artifacts: []Artifact{
		{Kind: "skill", Name: "p", Source: "plugin:team-forks", Files: []ArtifactFile{{Path: "SKILL.md", Content: []byte("{{path:docs}}")}}},
	}}
	if got := placeholderSources(m, ApplyOptions{}); len(got) != 0 {
		t.Errorf("an inactive plugin contributed %v", got)
	}
	got := placeholderSources(m, ApplyOptions{ActivePlugins: map[string]bool{"team-forks": true}})
	if want := map[string][]string{"path:docs": nil}; !reflect.DeepEqual(got, want) {
		t.Errorf("placeholderSources = %v, want %v", got, want)
	}
}
