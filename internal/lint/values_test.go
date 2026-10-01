package lint

import (
	"strings"
	"testing"
)

func TestValueSynonymFamiliesAreDisjoint(t *testing.T) {
	seen := map[string]string{}
	for fam, members := range ValueSynonymFamilies {
		for _, m := range members {
			n := NormValue(m)
			if other, dup := seen[n]; dup && other != fam {
				t.Errorf("%q is in both %q and %q", m, other, fam)
			}
			seen[n] = fam
			if _, collides := synonymOf[n]; collides {
				t.Errorf("value %q collides with a StandardFieldSynonyms key", m)
			}
		}
	}
}

func TestNormValue(t *testing.T) {
	for in, want := range map[string]string{"In corso": "in-corso", "  Done ": "done", "in_progress": "in-progress", "Città": "citta"} {
		if got := NormValue(in); got != want {
			t.Errorf("NormValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitProse(t *testing.T) {
	cases := []struct {
		in, token string
		prose     bool
	}{
		{"accepted — implemented in commit abc", "accepted", true},
		{"accepted for download — conditions apply", "accepted for download", true},
		{"superseded; see the new page", "superseded", true},
		{"decision-needed", "decision-needed", false},
		{"in corso", "in corso", false},
		{"baseline registered and campaign aggregated", "baseline", true},
	}
	for _, c := range cases {
		tok, _, ok := SplitProse(c.in)
		if ok != c.prose || (ok && tok != c.token) {
			t.Errorf("SplitProse(%q) = %q,%v", c.in, tok, ok)
		}
	}
}

func contractMap(t *testing.T, descriptor string) func(id, fm string) []Finding {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", descriptor)
	return func(id, fm string) []Finding {
		writeFile(t, k.DataRoot(), id+".md", "---\n"+fm+"\n---\n# T\n")
		f, err := Run(k, "", false)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
}

func TestInvalidFieldValueSetValueFix(t *testing.T) {
	write := contractMap(t, "---\ntype: Map\ntitle: M\nkind: journal\nfield_values.status: [done, in-progress]\nvalue_synonyms.done: [erledigt]\n---\n")
	findings := write("m/a", "type: Note\ntitle: A\nstatus: Completato")
	f := findingFor(findings, "m/a.md", "invalid_field_value")
	if f == nil || f.Fix == nil || f.Fix.Kind != FixSetValue || f.Fix.To != "done" {
		t.Fatalf("want set_value done, got %+v", f)
	}
	findings = write("m/b", "type: Note\ntitle: B\nstatus: In corso")
	if f := findingFor(findings, "m/b.md", "invalid_field_value"); f == nil || f.Fix == nil || f.Fix.To != "in-progress" {
		t.Fatalf("In corso: %+v", f)
	}
	findings = write("m/c", "type: Note\ntitle: C\nstatus: erledigt")
	if f := findingFor(findings, "m/c.md", "invalid_field_value"); f == nil || f.Fix == nil || f.Fix.To != "done" {
		t.Fatalf("contract synonym: %+v", f)
	}
	findings = write("m/d", "type: Note\ntitle: D\nstatus: mitigated")
	if f := findingFor(findings, "m/d.md", "invalid_field_value"); f == nil || f.Fix != nil {
		t.Fatalf("a value in no family must have no fix: %+v", f)
	}
}

func TestInvalidFieldValueTwoFamilyMembersNoFix(t *testing.T) {
	write := contractMap(t, "---\ntype: Map\ntitle: M\nfield_values.status: [done, completed]\n---\n")
	findings := write("m/a", "type: Note\ntitle: A\nstatus: finito")
	if f := findingFor(findings, "m/a.md", "invalid_field_value"); f == nil || f.Fix != nil {
		t.Fatalf("two candidates must give no fix: %+v", f)
	}
}

func TestMissingValueContractProposesFoldedStatus(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\nkind: journal\n---\n")
	statuses := []string{"done", "done", "done", "completato", "resolved", "in-progress", "in-progress", "in-corso", "open", "decision-needed"}
	for i, s := range statuses {
		writeFile(t, k.DataRoot(), "m/c"+string(rune('a'+i))+".md", "---\ntype: Task\ntitle: T\nstatus: "+s+"\n---\n# T\n")
	}
	findings, _ := Run(k, "", false)
	var p *Proposal
	for _, f := range findings {
		if f.Check == "missing_value_contract" && f.Proposal != nil && f.Proposal.Field == "status" {
			p = f.Proposal
		}
	}
	if p == nil {
		t.Fatal("no status proposal")
	}
	if strings.Join(p.Values, ",") != "decision-needed,done,in-progress,open,resolved" {
		t.Errorf("values = %v", p.Values)
	}
	if p.Mapping["completato"] != "done" || p.Mapping["in-corso"] != "in-progress" || len(p.Mapping) != 2 {
		t.Errorf("mapping = %v", p.Mapping)
	}
}

func TestProseValue(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
	writeFile(t, k.DataRoot(), "m/a.md", "---\ntype: Note\ntitle: A\nstatus: accepted — implemented in commit abc\n---\n# A\n")
	writeFile(t, k.DataRoot(), "m/b.md", "---\ntype: Note\ntitle: B\nstatus: done — after the second pass\n---\n# B\n")
	writeFile(t, k.DataRoot(), "m/c.md", "---\ntype: Note\ntitle: C\nstatus: decision-needed\n---\n# C\n")
	findings, _ := Run(k, "", false)
	if f := findingFor(findings, "m/a.md", "prose_value"); f == nil || f.Fix != nil {
		t.Errorf("accepted is in no family and there is no contract: finding without fix, got %+v", f)
	}
	if f := findingFor(findings, "m/b.md", "prose_value"); f == nil || f.Fix == nil || f.Fix.To != "done" {
		t.Errorf("done is a family member: want split_value done, got %+v", f)
	}
	if hasCheck(findings, "m/c.md", "prose_value") {
		t.Error("a hyphenated token is not prose")
	}
}
