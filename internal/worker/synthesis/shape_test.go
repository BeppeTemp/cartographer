package synthesis_test

import (
	"errors"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/worker/synthesis"
)

func hyp() synthesis.Hypothesis {
	return synthesis.Hypothesis{
		Map: "gaps", Kind: synthesis.KindOpenQuestion, Title: "Does a depend on b?",
		Involves: []string{"arch/a", "arch/b"}, Provenance: []string{"arch/a", "arch/b"},
		Confidence: "low", Date: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC),
		Evidence: []synthesis.Evidence{{ID: "arch/a", Says: "a calls b"}, {ID: "arch/b", Says: "b is called"}},
		Confirm:  "Check the call site; if so, link a to b.",
	}
}

func TestID_StableAcrossReplicasAndOrder(t *testing.T) {
	a := synthesis.ID("gaps", synthesis.KindOpenQuestion, []string{"x/b", "x/a", "x/a"})
	b := synthesis.ID("gaps", synthesis.KindOpenQuestion, []string{"x/a", "x/b"})
	if a != b {
		t.Fatalf("order changes the id: %s vs %s", a, b)
	}
	if synthesis.ID("gaps", synthesis.KindMissingContext, []string{"x/a", "x/b"}) == a {
		t.Error("the kind is not part of the id")
	}
	if want := "gaps/synthesis-open-question-"; a[:len(want)] != want || len(a) != len(want)+8 {
		t.Errorf("shape of %q", a)
	}
}

func TestCheck(t *testing.T) {
	if err := hyp().Check(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*synthesis.Hypothesis){
		"blocking kind":    func(h *synthesis.Hypothesis) { h.Kind = "factual" },
		"empty kind":       func(h *synthesis.Hypothesis) { h.Kind = "" },
		"no title":         func(h *synthesis.Hypothesis) { h.Title = " " },
		"7 involves":       func(h *synthesis.Hypothesis) { h.Involves = []string{"1", "2", "3", "4", "5", "6", "7"} },
		"bad confidence":   func(h *synthesis.Hypothesis) { h.Confidence = "certain" },
		"no map":           func(h *synthesis.Hypothesis) { h.Map = "" },
		"evidence uncited": func(h *synthesis.Hypothesis) { h.Evidence = []synthesis.Evidence{{ID: "arch/zzz", Says: "x"}} },
	}
	for name, mut := range cases {
		h := hyp()
		mut(&h)
		if h.Check() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestDropUncited(t *testing.T) {
	h := hyp()
	h.Evidence = append(h.Evidence, synthesis.Evidence{ID: "arch/c", Says: "not in provenance"}, synthesis.Evidence{ID: "arch/a"})
	h.DropUncited(map[string]bool{"arch/b": true}) // b vanished
	if len(h.Provenance) != 1 || h.Provenance[0] != "arch/a" || len(h.Evidence) != 1 {
		t.Fatalf("got %+v", h)
	}
	h.DropUncited(map[string]bool{"arch/a": true})
	if !errors.Is(h.Check(), synthesis.ErrNoEvidence) {
		t.Errorf("no provenance left must mean no write, got %v", h.Check())
	}
}

// The synthesis conventions against the real server: the gap kinds mirror the
// server's, and an open synthesis item does not block commit_gate on the pages
// it involves.
func TestHypothesisDoesNotBlockCommitGate(t *testing.T) {
	for _, k := range []string{synthesis.KindMissingContext, synthesis.KindOpenQuestion} {
		if !kb.IsGapKind(k) {
			t.Errorf("%s is not a server gap kind", k)
		}
	}
	if kb.IsGapKind("factual") || synthesis.IsGapKind("factual") {
		t.Error("a blocking kind passes")
	}
	k := kbtest.New(t)
	h := hyp()
	fm, err := okf.ParseFrontmatter("type: Contradiction")
	must(t, err)
	for key, v := range h.Frontmatter() {
		fm.Set(key, v)
	}
	_, err = k.WriteConcept(okf.ConceptID(h.ID()), fm, h.Body(), "")
	must(t, err)
	res, err := k.CommitGate([]okf.ConceptID{"arch/a", "arch/b"})
	must(t, err)
	if !res.Pass {
		t.Fatalf("an open_question hypothesis blocks: %v", res.Blockers)
	}
	// Control: the same item with a blocking kind does block.
	fm.Set("contradiction_kind", "factual")
	_, err = k.WriteConcept(okf.ConceptID(h.ID()), fm, h.Body(), "")
	must(t, err)
	if res, _ = k.CommitGate([]okf.ConceptID{"arch/a"}); res.Pass {
		t.Fatal("control: a blocking kind must block")
	}
}

// A rename on the server fails the build here instead of silently shrinking
// the allowlist.
func TestToolsExistInTheServerRegistry(t *testing.T) {
	s := mcpserver.New("test")
	mcpserver.RegisterKBTools(s, kbtest.New(t), mcpserver.Deps{BundleFS: fstest.MapFS{}})
	tools := s.Tools()
	for _, n := range synthesis.Tools() {
		if _, ok := tools[n]; !ok {
			t.Errorf("%s is not a registered tool", n)
		}
	}
	d := synthesis.Tools()
	slices.Sort(d)
	if len(slices.Compact(d)) != len(d) {
		t.Error("duplicate tool")
	}
}
