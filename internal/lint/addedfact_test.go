package lint

import (
	"fmt"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

const addedFactLine = "The nightly job runs at 02:00 and copies the data to the backup host."

// allLookup is the worst-case index: every concept is a candidate for every
// line, so only the exact verification stands between a hit and a finding.
func allLookup(t *testing.T, k *kb.KB, calls *int) func(string) []okf.ConceptID {
	t.Helper()
	return func(string) []okf.ConceptID {
		if calls != nil {
			*calls++
		}
		return []okf.ConceptID{"m/a", "m/b", "m/c"}
	}
}

func putConcept(t *testing.T, k *kb.KB, id, body string) {
	t.Helper()
	writeFile(t, k.DataRoot(), id+".md", "---\ntype: Note\ntitle: "+id+"\n---\n"+body+"\n")
}

func TestAddedRepeatedFacts(t *testing.T) {
	k := tempKB(t)
	putConcept(t, k, "m/a", addedFactLine)
	putConcept(t, k, "m/b", "Intro.\n\n"+addedFactLine)
	putConcept(t, k, "m/c", "# C\n\n"+addedFactLine)

	got, used := AddedRepeatedFacts(k, "m/c", "", "# C\n\n"+addedFactLine, 20, allLookup(t, k, nil))
	if len(got) != 1 || used != 1 {
		t.Fatalf("want one finding in one lookup: %+v used=%d", got, used)
	}
	f := got[0]
	if f.Check != "repeated_fact" || f.Severity != SevInfo || f.Path != "m/c.md" || f.Fix != nil {
		t.Fatalf("shape: %+v", f)
	}
	if !strings.Contains(f.Message, "m/a, m/b") || !strings.Contains(f.Message, "nightly job") {
		t.Fatalf("message: %s", f.Message)
	}

	// An unchanged line on a rewrite, and a moved line, are not added.
	if got, _ := AddedRepeatedFacts(k, "m/c", "# C\n\n"+addedFactLine, "# C\n\n"+addedFactLine+"\n\nMore.", 20, allLookup(t, k, nil)); len(got) != 0 {
		t.Fatalf("unchanged line reported: %+v", got)
	}
	moved := "Other prose that is long enough to be a fact line of its own here.\n\n" + addedFactLine
	if got, _ := AddedRepeatedFacts(k, "m/c", addedFactLine+"\n\nOther prose that is long enough to be a fact line of its own here.", moved, 20, allLookup(t, k, nil)); len(got) != 0 {
		t.Fatalf("moved line reported: %+v", got)
	}
}

func TestAddedRepeatedFactsThreshold(t *testing.T) {
	k := tempKB(t)
	putConcept(t, k, "m/a", addedFactLine)
	putConcept(t, k, "m/c", addedFactLine)
	if got, _ := AddedRepeatedFacts(k, "m/c", "", addedFactLine, 20, allLookup(t, k, nil)); len(got) != 0 {
		t.Fatalf("two owners under the default threshold of 3: %+v", got)
	}
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\nrepeated_fact_min: 2\n---\n")
	got, _ := AddedRepeatedFacts(k, "m/c", "", addedFactLine, 20, allLookup(t, k, nil))
	if len(got) != 1 || !strings.Contains(got[0].Message, "m/a") {
		t.Fatalf("repeated_fact_min: 2: %+v", got)
	}
}

func TestAddedRepeatedFactsNotFacts(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "templates/note.md", "---\ntype: Note\n---\n> Fill in the details of this note before saving it anywhere.\n")
	lines := map[string]string{
		"short":    "Too short to count.",
		"heading":  "## A heading that is certainly longer than forty bytes in total",
		"code":     "```\n" + addedFactLine + "\n```",
		"template": "> Fill in the details of this note before saving it anywhere.",
	}
	for name, line := range lines {
		for _, id := range []string{"m/a", "m/b", "m/c"} {
			putConcept(t, k, id, line)
		}
		if got, _ := AddedRepeatedFacts(k, "m/c", "", line, 20, allLookup(t, k, nil)); len(got) != 0 {
			t.Errorf("%s: reported %+v", name, got)
		}
	}
}

// TestAddedRepeatedFactsNearMiss: the index returns a candidate that shares
// the terms but not the exact line; verification drops it (D351).
func TestAddedRepeatedFactsNearMiss(t *testing.T) {
	k := tempKB(t)
	putConcept(t, k, "m/a", addedFactLine)
	putConcept(t, k, "m/b", "The nightly job runs at 03:00 and copies the data to the backup host.")
	putConcept(t, k, "m/c", addedFactLine)
	if got, _ := AddedRepeatedFacts(k, "m/c", "", addedFactLine, 20, allLookup(t, k, nil)); len(got) != 0 {
		t.Fatalf("near miss counted as an owner: %+v", got)
	}
}

func TestAddedRepeatedFactsCaps(t *testing.T) {
	k := tempKB(t)
	var body []string
	for i := 0; i < 30; i++ {
		body = append(body, fmt.Sprintf("Distinct fact number %02d that every concept of this fixture repeats verbatim.", i))
	}
	text := strings.Join(body, "\n\n")
	for _, id := range []string{"m/a", "m/b", "m/c"} {
		putConcept(t, k, id, text)
	}
	calls := 0
	got, used := AddedRepeatedFacts(k, "m/c", "", text, 20, allLookup(t, k, &calls))
	if len(got) != repeatedFactPerConceptCap || used != calls || used > repeatedFactLookupCap {
		t.Fatalf("findings=%d used=%d calls=%d", len(got), used, calls)
	}
	// The lookup budget stops the scan even when nothing matches.
	calls = 0
	none := func(string) []okf.ConceptID { calls++; return nil }
	if _, used := AddedRepeatedFacts(k, "m/c", "", text, 7, none); used != 7 || calls != 7 {
		t.Fatalf("budget 7: used=%d calls=%d", used, calls)
	}
}

func TestAddedRepeatedFactsLintIgnore(t *testing.T) {
	k := tempKB(t)
	putConcept(t, k, "m/a", addedFactLine)
	putConcept(t, k, "m/b", addedFactLine)
	writeFile(t, k.DataRoot(), "m/c.md", "---\ntype: Note\ntitle: C\nlint_ignore: [repeated_fact]\n---\n"+addedFactLine+"\n")
	if got, _ := AddedRepeatedFacts(k, "m/c", "", addedFactLine, 20, allLookup(t, k, nil)); len(got) != 0 {
		t.Fatalf("concept lint_ignore: %+v", got)
	}
	putConcept(t, k, "m/c", addedFactLine)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\nlint_ignore: [repeated_fact]\n---\n")
	if got, _ := AddedRepeatedFacts(k, "m/c", "", addedFactLine, 20, allLookup(t, k, nil)); len(got) != 0 {
		t.Fatalf("map lint_ignore: %+v", got)
	}
}
