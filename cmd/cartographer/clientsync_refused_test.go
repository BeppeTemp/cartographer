package main

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// A refusal that is not a symlink one (a rules file with its own frontmatter)
// prints its own cause only: the symlink sentence would be wrong about a regular
// file, and the warning that follows is right.
func TestPrintApplySummary_RefusalNamesOnlyItsCause(t *testing.T) {
	foreign := contract.Artifact{Kind: "instructions", Name: "kb-a", Source: "kb:kb-a"}
	link := contract.Artifact{Kind: "skill", Name: "tidy", Source: "kb:kb-a"}
	res := provisioning.AppliedResult{
		Refused: []contract.Artifact{foreign, link},
		Warnings: []string{
			"instructions/kb-a not installed: .agents/rules/cartographer.md: rules file has frontmatter without the required header; left untouched, add `trigger: always_on` to its frontmatter or remove the file",
			"skill/tidy not installed: destination is a symlink",
		},
	}
	var w strings.Builder
	printApplySummary(&w, t.TempDir(), map[string]provisioning.AppliedResult{"antigravity": res}, false, nil)
	out := w.String()
	if strings.Contains(out, "refused: instructions/kb-a") {
		t.Errorf("a regular file is reported as a symlink:\n%s", out)
	}
	if !strings.Contains(out, "trigger: always_on") {
		t.Errorf("the right cause is missing:\n%s", out)
	}
	if !strings.Contains(out, "refused: skill/tidy [kb:kb-a] — its destination is a symlink") {
		t.Errorf("a symlink refusal lost its line:\n%s", out)
	}
}
