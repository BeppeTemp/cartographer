package provisioning

import (
	"strings"
	"testing"
)

func TestMergeInstructionsParts(t *testing.T) {
	a := InstructionsParts{Provider: "claude", Shared: "S", Env: "env-c", CLI: "cli",
		Sections: []InstructionsSection{{"kb-b", "B"}, {"kb-a", "A"}}}
	b := InstructionsParts{Provider: "codex", Shared: "S", Env: "env-x", CLI: "cli",
		Sections: []InstructionsSection{{"kb-a", "A"}, {"kb-c", "C"}}}
	got := MergeInstructionsParts([]InstructionsParts{a, b})
	if names := got.Sections; len(names) != 3 || names[0].Name != "kb-b" || names[1].Name != "kb-a" || names[2].Name != "kb-c" {
		t.Errorf("sections = %v", names)
	}
	if got.CLI != "cli" {
		t.Errorf("equal per-client text kept once, got %q", got.CLI)
	}
	if !strings.Contains(got.Env, "For claude:\nenv-c") || !strings.Contains(got.Env, "For codex:\nenv-x") {
		t.Errorf("env not labelled by client: %q", got.Env)
	}
	if r := MergeInstructionsParts([]InstructionsParts{a}); r.Render() != a.Render() {
		t.Error("merging one block changes it")
	}
}

// The merge keeps the most restrictive sharer's choice (#2449).
func TestMergeInstructionsParts_MostRestrictive(t *testing.T) {
	open := InstructionsParts{Provider: "claude", Paths: "TABLE", Env: "names kb-x",
		Sections: []InstructionsSection{{"kb-a", "A"}, {"kb-x", "X"}}}
	tight := InstructionsParts{Provider: "codex", Paths: "NO TABLE", Env: "env", Restricted: true, WithheldKBs: []string{"kb-x"},
		Sections: []InstructionsSection{{"kb-a", "A"}}}
	for _, order := range [][]InstructionsParts{{open, tight}, {tight, open}} {
		got := MergeInstructionsParts(order)
		if got.Paths != "NO TABLE" {
			t.Errorf("paths = %q, want the restricted sharer's", got.Paths)
		}
		for _, s := range got.Sections {
			if s.Name == "kb-x" {
				t.Error("a KB withheld by one sharer is in the merge")
			}
		}
		if strings.Contains(got.Env, "kb-x") {
			t.Errorf("env of an unrestricted sharer leaked: %q", got.Env)
		}
	}
}
