package lint

import (
	"fmt"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// proposalKB: ten Host pages with Purpose, Operations and Recovery in every one,
// Notes in four of them (40%), Trivia in one; plus two Runbook pages (too few).
func proposalKB(t *testing.T) *kb.KB {
	t.Helper()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "infra/_map.md", "---\ntype: Map\nkind: map\ntitle: Infra\n---\n")
	for i := 0; i < 10; i++ {
		body := "## Purpose\n\np\n\n## Operations\n\no\n\n## Recovery\n\nr\n"
		if i < 4 {
			body += "\n## Notes\n\nn\n"
		}
		if i == 0 {
			body += "\n## Trivia\n\nt\n"
		}
		state := []string{"up", "down"}[i%2]
		writeFile(t, k.DataRoot(), fmt.Sprintf("infra/h%d.md", i), "---\ntype: Host\ntitle: H"+fmt.Sprint(i)+"\nowner: me\nstate: "+state+"\n---\n# H\n\n"+body)
	}
	for i := 0; i < 2; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("infra/r%d.md", i), "---\ntype: Runbook\ntitle: R\n---\n# R\n\n## Steps\n")
	}
	return k
}

func TestTemplateProposal(t *testing.T) {
	k := proposalKB(t)
	got := itemsOf(review(t, k), ReviewTemplateProposal)
	if len(got) != 1 {
		t.Fatalf("want one proposal (Host only: Runbook has 2 pages), got %+v", got)
	}
	it := got[0]
	if it.TemplateSlug != "host" || len(it.Concepts) != 1 || it.Concepts[0] != "infra/_map" {
		t.Fatalf("item: %+v", it)
	}
	info, ok := kb.ParseTemplate("host", it.Template)
	if !ok {
		t.Fatalf("proposal text does not parse:\n%s", it.Template)
	}
	if strings.Join(info.Sections, ",") != "Purpose,Operations,Recovery,Notes" {
		t.Errorf("sections = %v (Trivia is under the 20%% floor)", info.Sections)
	}
	if len(info.RequiredSections()) != 3 || strings.Join(info.OptionalSections, ",") != "Notes" {
		t.Errorf("3 required and Notes optional, got required %v optional %v", info.RequiredSections(), info.OptionalSections)
	}
	if strings.Join(info.RequiredFields, ",") != "owner,state" {
		t.Errorf("required fields = %v", info.RequiredFields)
	}
	if v := strings.Join(info.FieldValues["state"], ","); v != "down,up" {
		t.Errorf("state values = %q", v)
	}
	if it.MapUpdate["map"] != "infra" || it.MapUpdate["default_template"] != "host" || it.MapUpdate["require_template"] != true {
		t.Errorf("map_update args: %+v", it.MapUpdate)
	}
	// Deterministic: the same KB gives the same bytes.
	again := itemsOf(review(t, k), ReviewTemplateProposal)
	if len(again) != 1 || again[0].Template != it.Template {
		t.Error("the proposal is not byte-identical across runs")
	}
}

func TestTemplateProposalOnePerType(t *testing.T) {
	k := proposalKB(t)
	for i := 0; i < 3; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("infra/x%d.md", i), "---\ntype: Runbook\ntitle: X\n---\n# X\n\n## Steps\n\ns\n")
	}
	got := itemsOf(review(t, k), ReviewTemplateProposal)
	if len(got) != 2 {
		t.Fatalf("one proposal per type with 3+ pages, got %d: %+v", len(got), got)
	}
}

func TestTemplateProposalSilentOnceBound(t *testing.T) {
	k := proposalKB(t)
	writeFile(t, k.Root, "templates/host.md", "---\ntype: Host\ntitle: \"{{title}}\"\n---\n# {{title}}\n\n## Purpose\n")
	writeFile(t, k.DataRoot(), "infra/_map.md", "---\ntype: Map\nkind: map\ntitle: Infra\ntemplates: [host]\n---\n")
	for _, it := range itemsOf(review(t, k), ReviewTemplateProposal) {
		if it.TemplateSlug == "host" {
			t.Errorf("Host pages resolve to templates/host.md: nothing to propose: %+v", it)
		}
	}
}
