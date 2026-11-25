package mcpserver

import (
	"strings"
	"testing"
)

func TestCheckPresetCreate(t *testing.T) {
	t.Parallel()
	ok := PresetCreate{Title: "Runbooks", Kind: "map", Templates: []string{"runbook"}, DefaultTemplate: "runbook"}
	if err := CheckPresetCreate(ok, []string{"runbook"}); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*PresetCreate){
		"no title":         func(c *PresetCreate) { c.Title = " " },
		"bad kind":         func(c *PresetCreate) { c.Kind = "folder" },
		"empty field name": func(c *PresetCreate) { c.RequiredFields = map[string][]string{"*": {""}} },
		"missing template": func(c *PresetCreate) { c.Templates = []string{"other"}; c.DefaultTemplate = "" },
		"default outside":  func(c *PresetCreate) { c.DefaultTemplate = "other" },
	} {
		c := ok
		mut(&c)
		if err := CheckPresetCreate(c, []string{"runbook"}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if ValidateTemplate([]byte(testTemplate)) != nil || ValidateTemplate([]byte("---\ntype: Note\nx-template.nope: [a]\n---\n# H\n")) == nil {
		t.Error("ValidateTemplate must be artifact_write's judgement")
	}
}

// AC-plugdash-1
func TestCheckPreset(t *testing.T) {
	t.Parallel()
	full := PresetCreate{Title: "Runbooks", Kind: "map", Templates: []string{"runbook"}, DefaultTemplate: "runbook"}
	for _, tc := range []struct {
		name       string
		c          PresetCreate
		slugs      []string
		dashboards int
		want       string // substring of the error, "" for accepted
	}{
		{"nothing", PresetCreate{}, nil, 0, "needs a map contract, dashboards or both"},
		{"dashboards only", PresetCreate{}, nil, 1, ""},
		{"template without contract", PresetCreate{}, []string{"runbook"}, 1, "templates need a map contract"},
		{"title without kind", PresetCreate{Title: "Runbooks"}, nil, 1, `kind must be "map" or "journal"`},
		{"full contract with dashboards", full, []string{"runbook"}, 2, ""},
	} {
		err := CheckPreset(tc.c, tc.slugs, tc.dashboards)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.want)
		}
	}
}
