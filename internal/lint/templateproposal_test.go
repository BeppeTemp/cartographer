package lint

import (
	"fmt"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// proposalKB: ten Host pages with Purpose, Operations and Recovery in every one,
// Notes in five of them (50%), Trivia in one; plus two Runbook pages (too few).
func proposalKB(t *testing.T) *kb.KB {
	t.Helper()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "infra/_map.md", "---\ntype: Map\nkind: map\ntitle: Infra\n---\n")
	for i := 0; i < 10; i++ {
		body := "## Purpose\n\np\n\n## Operations\n\no\n\n## Recovery\n\nr\n"
		if i < 5 {
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
		t.Errorf("sections = %v (Trivia is under the 50%% floor)", info.Sections)
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

// twoMapKB: two maps whose Topic pages bind to the one slug "topic". Every page
// carries Purpose; "Lonely" is one page of the four of alpha; alpha's pages are
// active and beta's deprecated; a heading with a run of spaces is the same
// section as its single-space spelling.
func twoMapKB(t *testing.T) *kb.KB {
	t.Helper()
	k := tempKB(t)
	for _, m := range []string{"alpha", "beta"} {
		writeFile(t, k.DataRoot(), m+"/_map.md", "---\ntype: Map\nkind: map\ntitle: "+m+"\n---\n")
	}
	for i := 0; i < 4; i++ {
		body := "## Purpose\n\np\n"
		if i == 0 {
			body += "\n## Lonely\n\nl\n"
		}
		writeFile(t, k.DataRoot(), fmt.Sprintf("alpha/a%d.md", i), "---\ntype: Topic\ntitle: A\nstatus: active\ntags: [x]\n---\n# A\n\n"+body)
	}
	for i := 0; i < 4; i++ {
		h := "Purpose"
		if i%2 == 1 {
			h = "Purpose  "
		}
		writeFile(t, k.DataRoot(), fmt.Sprintf("beta/b%d.md", i), "---\ntype: Topic\ntitle: B\nstatus: deprecated\ntags: [x]\n---\n# B\n\n## "+h+"\n\np\n")
	}
	return k
}

func TestTemplateProposalOnePerSlugAcrossMaps(t *testing.T) {
	got := itemsOf(review(t, twoMapKB(t)), ReviewTemplateProposal)
	if len(got) != 1 {
		t.Fatalf("two maps, one slug: want one proposal, got %d: %+v", len(got), got)
	}
	it := got[0]
	if strings.Join(it.Concepts, ",") != "alpha/_map,beta/_map" || it.Weight != 8 || len(it.MapUpdates) != 2 {
		t.Fatalf("item should list both maps and carry one map_update each: %+v", it)
	}
	info, ok := kb.ParseTemplate("topic", it.Template)
	if !ok {
		t.Fatalf("does not parse:\n%s", it.Template)
	}
	if strings.Join(info.Sections, ",") != "Purpose" {
		t.Errorf("sections = %v: Lonely is one page of eight", info.Sections)
	}
	if _, has := info.FieldValues["status"]; has {
		t.Errorf("status differs per map, it belongs to the map contracts: %v", info.FieldValues)
	}
	if strings.Join(info.RequiredFields, ",") != "status,tags" {
		t.Errorf("required fields = %v", info.RequiredFields)
	}
}
