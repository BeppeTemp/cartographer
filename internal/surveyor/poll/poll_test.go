package poll

import (
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

func TestExecutorsAreTheKBVocabulary(t *testing.T) {
	if !reflect.DeepEqual(Executors, kb.IngestExecutors) {
		t.Fatalf("Executors = %v, kb.IngestExecutors = %v", Executors, kb.IngestExecutors)
	}
}

func TestUnitIDIsStableAndTwoSegments(t *testing.T) {
	a := UnitID("ingest", "sources/f", "https://git.example.com/r#docs/A b.md", "rev1", "docs/A b.md")
	if a != UnitID("ingest", "sources/f", "https://git.example.com/r#docs/A b.md", "rev1", "docs/A b.md") {
		t.Fatal("not deterministic")
	}
	if a == UnitID("ingest", "sources/f", "https://git.example.com/r#docs/A b.md", "rev2", "docs/A b.md") {
		t.Fatal("the revision does not change the ID")
	}
	if strings.Count(a, "/") != 1 || !strings.HasPrefix(a, "ingest/docs-a-b-md-") || len(a) != len("ingest/docs-a-b-md-")+8 {
		t.Fatalf("id = %q", a)
	}
	long := UnitID("m", "f", "l", "r", strings.Repeat("abc/", 30))
	if slug := strings.TrimPrefix(long, "m/"); len(slug) > 40+1+8 {
		t.Fatalf("slug not cut: %q", long)
	}
	if id := UnitID("m", "f", "l", "r", "///"); !strings.HasPrefix(id, "m/entry-") {
		t.Fatalf("empty slug: %q", id)
	}
}

func TestParseAnswer(t *testing.T) {
	ok := `Here you go: {"targets":["kb/a"],"extract":"facts","verify":"links resolve","executor":"any","body":"brief"} done`
	a, err := ParseAnswer(ok, []string{"kb"})
	if err != nil || a.Targets[0] != "kb/a" || a.Executor != "any" {
		t.Fatalf("%+v %v", a, err)
	}
	long := strings.Repeat("x", 501)
	for name, tc := range map[string]struct{ in, want string }{
		"no json":        {"nothing", "not a JSON object"},
		"bad json":       {"{nope}", "not valid JSON"},
		"executor root":  {`{"targets":["kb/a"],"extract":"e","verify":"v","executor":"root","body":"b"}`, "executor"},
		"outside maps":   {`{"targets":["other/a"],"extract":"e","verify":"v","executor":"any","body":"b"}`, "outside the plan's maps"},
		"no targets":     {`{"targets":[],"extract":"e","verify":"v","executor":"any","body":"b"}`, "targets"},
		"dot dot":        {`{"targets":["kb/../a"],"extract":"e","verify":"v","executor":"any","body":"b"}`, "concept ID"},
		"extension":      {`{"targets":["kb/a.md"],"extract":"e","verify":"v","executor":"any","body":"b"}`, "concept ID"},
		"two lines":      {`{"targets":["kb/a"],"extract":"e\nf","verify":"v","executor":"any","body":"b"}`, "one line"},
		"long verify":    {`{"targets":["kb/a"],"extract":"e","verify":"` + long + `","executor":"any","body":"b"}`, "one line"},
		"empty body":     {`{"targets":["kb/a"],"extract":"e","verify":"v","executor":"any","body":" "}`, "body"},
		"too many":       {`{"targets":["kb/1","kb/2","kb/3","kb/4","kb/5","kb/6","kb/7","kb/8","kb/9","kb/10","kb/11","kb/12","kb/13","kb/14","kb/15","kb/16","kb/17","kb/18","kb/19","kb/20","kb/21"],"extract":"e","verify":"v","executor":"any","body":"b"}`, "targets"},
		"single segment": {`{"targets":["a"],"extract":"e","verify":"v","executor":"any","body":"b"}`, "concept ID"},
	} {
		if _, err := ParseAnswer(tc.in, []string{"kb"}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
	// A plan with no maps accepts any map.
	if _, err := ParseAnswer(strings.Replace(ok, "kb/a", "any/a", 1), nil); err != nil {
		t.Errorf("no maps: %v", err)
	}
}
