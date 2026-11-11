package mcpserver

import (
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
