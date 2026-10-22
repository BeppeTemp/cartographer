package provisioning

import (
	"strings"
	"testing"
)

// One warning per KB that carries a pre-D958 bundled-skill name, however many
// of them it carries; a current name, a user's own skill and another kind of
// artifact stay quiet.
func TestLegacySkillWarnings_OncePerKB(t *testing.T) {
	t.Parallel()
	m := Manifest{Artifacts: []Artifact{
		{Source: "kb:kb-a", Kind: "skill", Name: "kb-create"},
		{Source: "kb:kb-a", Kind: "skill", Name: "kb-ingest"},
		{Source: "kb:kb-a", Kind: "skill", Name: "cartographer-kb-create"},
		{Source: "kb:kb-a", Kind: "skill", Name: "my-own"},
		{Source: "kb:kb-b", Kind: "agent", Name: "kb-create"},
		{Source: "kb:kb-c", Kind: "skill", Name: "kb-doctor"},
	}}
	got := legacySkillWarnings(m)
	if len(got) != 2 {
		t.Fatalf("want one warning for kb-a and one for kb-c, got %v", got)
	}
	if !strings.Contains(got[0], `"kb-a"`) || !strings.Contains(got[0], "kb-create, kb-ingest") || !strings.Contains(got[0], "legacy_skill_name") {
		t.Errorf("kb-a warning = %q", got[0])
	}
	if !strings.Contains(got[1], `"kb-c"`) {
		t.Errorf("kb-c warning = %q", got[1])
	}
}
