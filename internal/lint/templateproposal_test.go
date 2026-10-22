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
	t.Parallel()
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
	if it.MapUpdate["map"] != "infra" || it.MapUpdate["default_template"] != "host" {
		t.Errorf("map_update args: %+v", it.MapUpdate)
	}
	// Deterministic: the same KB gives the same bytes.
	again := itemsOf(review(t, k), ReviewTemplateProposal)
	if len(again) != 1 || again[0].Template != it.Template {
		t.Error("the proposal is not byte-identical across runs")
	}
}

func TestTemplateProposalOnePerType(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// Two type spellings in one map share a slug: the proposal names that map once.
func TestTemplateProposalOneMapTwoSpellings(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\nkind: map\ntitle: Ops\n---\n")
	for i, typ := range []string{"Runbook", "Runbook", "Runbook", "runbook", "runbook", "runbook"} {
		writeFile(t, k.DataRoot(), fmt.Sprintf("ops/r%d.md", i), "---\ntype: "+typ+"\ntitle: R\n---\n# R\n\n## Steps\n\ns\n")
	}
	got := itemsOf(review(t, k), ReviewTemplateProposal)
	if len(got) != 1 || strings.Join(got[0].Concepts, ",") != "ops/_map" || len(got[0].MapUpdates) > 1 {
		t.Fatalf("want one proposal naming ops once: %+v", got)
	}
}

const hostTemplate = "---\ntype: Host\ntitle: \"{{title}}\"\nx-template.required_fields: [owner]\n---\n# {{title}}\n\n## Purpose\n"

func smallHostKB(t *testing.T, pages ...string) *kb.KB {
	t.Helper()
	k := tempKB(t)
	writeFile(t, k.Root, "templates/host.md", hostTemplate)
	writeFile(t, k.DataRoot(), "infra/_map.md", "---\ntype: Map\nkind: map\ntitle: Infra\n---\n")
	for i, fm := range pages {
		writeFile(t, k.DataRoot(), fmt.Sprintf("infra/h%d.md", i), "---\ntype: Host\ntitle: H\n"+fm+"---\n# H\n\n## Purpose\n\np\n")
	}
	return k
}

// The floor of three pages is for inducing a template: one that exists is
// proposed for two pages that follow it, and for none that do not.
func TestTemplateProposalExistingTemplateSmallType(t *testing.T) {
	t.Parallel()
	got := itemsOf(review(t, smallHostKB(t, "owner: me\n", "owner: you\n")), ReviewTemplateProposal)
	if len(got) != 1 || got[0].TemplateSlug != "host" || got[0].Template != "" {
		t.Fatalf("want one item naming templates/host.md without text: %+v", got)
	}
	if !strings.Contains(got[0].Evidence, `2 Host pages in map "infra" follow templates/host.md, which the map does not declare`) {
		t.Errorf("evidence: %q", got[0].Evidence)
	}
	if strings.Contains(got[0].SuggestedAction, "binds the pages") {
		t.Errorf("action keeps the phrase that misdescribes binding: %q", got[0].SuggestedAction)
	}

	if got := itemsOf(review(t, smallHostKB(t, "owner: me\n", "")), ReviewTemplateProposal); len(got) != 0 {
		t.Errorf("one of two pages misses a required field (50%% < 60%%): %+v", got)
	}

	// No template to follow: the floor stays for induction.
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "infra/_map.md", "---\ntype: Map\nkind: map\ntitle: Infra\n---\n")
	for i := 0; i < 2; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("infra/h%d.md", i), "---\ntype: Host\ntitle: H\nowner: me\n---\n# H\n\n## Purpose\n")
	}
	if got := itemsOf(review(t, k), ReviewTemplateProposal); len(got) != 0 {
		t.Errorf("two pages and no template: nothing to induce from: %+v", got)
	}

	// The map already declares it.
	k = smallHostKB(t, "owner: me\n", "owner: you\n")
	writeFile(t, k.DataRoot(), "infra/_map.md", "---\ntype: Map\nkind: map\ntitle: Infra\ntemplates: [host]\n---\n")
	if got := itemsOf(review(t, k), ReviewTemplateProposal); len(got) != 0 {
		t.Errorf("the map declares the slug: %+v", got)
	}
}

// A proposal never drops a value of the map's own vocabulary because the pages
// happen not to use it; a field the map leaves free keeps the pages' values.
func TestTemplateProposalValuesFromMapContract(t *testing.T) {
	t.Parallel()
	build := func(mapFM string) []ReviewItem {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "infra/_map.md", "---\ntype: Map\nkind: map\ntitle: Infra\n"+mapFM+"---\n")
		for i, st := range []string{"open", "closed", "open"} {
			writeFile(t, k.DataRoot(), fmt.Sprintf("infra/h%d.md", i), "---\ntype: Host\ntitle: H\nstatus: "+st+"\n---\n# H\n\n## Purpose\n")
		}
		return itemsOf(review(t, k), ReviewTemplateProposal)
	}
	values := func(it ReviewItem) string {
		info, ok := kb.ParseTemplate("host", it.Template)
		if !ok {
			t.Fatalf("does not parse:\n%s", it.Template)
		}
		return strings.Join(info.FieldValues["status"], ",")
	}
	got := build("field_values.status: [open, closed, parked]\n")
	if len(got) != 1 || values(got[0]) != "closed,open,parked" {
		t.Fatalf("the map's whole vocabulary belongs in the template: %+v", got)
	}
	got = build("")
	if len(got) != 1 || values(got[0]) != "closed,open" {
		t.Fatalf("a free field keeps the pages' values: %+v", got)
	}
}

func TestTemplateProposalMapsConstrainingDifferentlyGiveNoValues(t *testing.T) {
	t.Parallel()
	k := twoMapKB(t)
	writeFile(t, k.DataRoot(), "alpha/_map.md", "---\ntype: Map\nkind: map\ntitle: alpha\nfield_values.status: [active, paused]\n---\n")
	writeFile(t, k.DataRoot(), "beta/_map.md", "---\ntype: Map\nkind: map\ntitle: beta\nfield_values.status: [deprecated, retired]\n---\n")
	got := itemsOf(review(t, k), ReviewTemplateProposal)
	if len(got) != 1 {
		t.Fatalf("want one proposal: %+v", got)
	}
	info, _ := kb.ParseTemplate("topic", got[0].Template)
	if _, has := info.FieldValues["status"]; has {
		t.Errorf("the maps disagree: %v", info.FieldValues)
	}
}

// The preview runs lint's own checks: what it counts is what lint reports once
// the proposal is applied (the trap: a preview computed any other way drifts).
func TestTemplateProposalImpactMatchesLintAfterApplying(t *testing.T) {
	t.Parallel()
	k := proposalKB(t)
	got := itemsOf(review(t, k), ReviewTemplateProposal)
	if len(got) != 1 || len(got[0].Impact) != 1 {
		t.Fatalf("want one proposal with one impact entry: %+v", got)
	}
	it, im := got[0], got[0].Impact[0]
	if im.Map != "infra" || im.Pages != 3 || im.ByCheck["template_missing"] != 2 {
		t.Fatalf("the two Runbook pages gain template_missing and a Host page the extra section: %+v", im)
	}
	if !strings.Contains(it.Evidence, "declaring it makes 3 pages of the map fail (template_missing: 2, template_extra_section: 1)") {
		t.Errorf("evidence: %q", it.Evidence)
	}
	if len(im.Examples) != 3 || im.Examples[0] != "infra/h0" {
		t.Errorf("examples: %v", im.Examples)
	}

	writeFile(t, k.Root, "templates/host.md", it.Template)
	writeFile(t, k.DataRoot(), "infra/_map.md", "---\ntype: Map\nkind: map\ntitle: Infra\ntemplates: [host]\ndefault_template: host\n---\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	lintByCheck := map[string]int{}
	for _, f := range findings {
		if strings.HasPrefix(f.Path, "infra/") && strings.HasPrefix(f.Check, "template_") {
			lintByCheck[f.Check]++
		}
	}
	if fmt.Sprint(lintByCheck) != fmt.Sprint(im.ByCheck) {
		t.Errorf("preview %v, lint after applying %v", im.ByCheck, lintByCheck)
	}
}

// Impact reads every page of a map: a caller scoped to part of it (the map
// descriptor and one page visible) gets the proposal without ids or counts of
// the pages it cannot see; a whole-KB caller keeps the preview.
func TestTemplateProposalImpactHiddenFromScopedCaller(t *testing.T) {
	t.Parallel()
	got := itemsOf(review(t, proposalKB(t)), ReviewTemplateProposal)
	if len(got) != 1 || len(got[0].Impact) == 0 {
		t.Fatalf("want a proposal with impact: %+v", got)
	}
	visible := func(id string) bool { return id == "infra/_map" || id == "infra/h0" }
	scoped := FilterReview(got, visible, false)
	if len(scoped) != 1 {
		t.Fatalf("the proposal itself stays visible: %+v", scoped)
	}
	if len(scoped[0].Impact) != 0 || strings.Contains(scoped[0].Evidence, "declaring it") || strings.Contains(scoped[0].Evidence, "infra/h") {
		t.Errorf("impact leaked to a scoped caller: %+v", scoped[0])
	}
	whole := FilterReview(got, visible, true)
	if len(whole[0].Impact) == 0 || !strings.Contains(whole[0].Evidence, "declaring it") {
		t.Errorf("whole caller lost the preview: %+v", whole[0])
	}
}

func TestTemplateProposalImpactEmptyWhenEverythingFollows(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "infra/_map.md", "---\ntype: Map\nkind: map\ntitle: Infra\n---\n")
	for i := 0; i < 3; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("infra/h%d.md", i), "---\ntype: Host\ntitle: H\n---\n# H\n\n## Purpose\n\np\n")
	}
	got := itemsOf(review(t, k), ReviewTemplateProposal)
	if len(got) != 1 || len(got[0].Impact) != 0 || strings.Contains(got[0].Evidence, "declaring it") {
		t.Fatalf("no page fails: no impact: %+v", got)
	}
}
