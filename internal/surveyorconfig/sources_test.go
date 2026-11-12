package surveyorconfig

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const testDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestScrubSourcesKeepsWellFormedAndDatesTheCheck(t *testing.T) {
	recv := time.Date(2031, 2, 3, 12, 0, 0, 0, time.UTC)
	in := []Source{
		{Name: "wiki-lister", Origin: OriginOperator, Kind: KindExec, Shape: "feed", Check: SourceCheck{Outcome: CheckOK, AgeSeconds: 90, At: time.Unix(1, 0)}},
		{Name: "acme/pages", Origin: OriginPlugin, Plugin: "acme", Digest: testDigest, Kind: KindStarlark, Shape: "system", Check: SourceCheck{Outcome: CheckFailed, Code: "identity_not_read_only", AgeSeconds: 5}},
		{Name: "acme/wiki-mcp", Origin: OriginPlugin, Plugin: "acme", Digest: testDigest, Kind: KindMCP, Check: SourceCheck{Outcome: CheckUnsupported}},
		{Name: "git-docs", Origin: OriginCore, Kind: KindBuiltin, Shape: "feed", Check: SourceCheck{Outcome: CheckNever}},
	}
	got := ScrubSources(in, recv)
	if len(got) != 4 {
		t.Fatalf("kept %d of 4: %+v", len(got), got)
	}
	// The sender's own time is never kept: At is the receipt minus the age.
	if want := recv.Add(-90 * time.Second); !got[0].Check.At.Equal(want) || got[0].Check.AgeSeconds != 0 {
		t.Errorf("check = %+v, want At %s and no age", got[0].Check, want)
	}
	if !got[1].Check.At.Equal(recv.Add(-5 * time.Second)) {
		t.Errorf("failed check At = %v", got[1].Check.At)
	}
	if !got[2].Check.At.IsZero() || !got[3].Check.At.IsZero() {
		t.Error("an unsupported or never check has no time")
	}
	raw, _ := json.Marshal(got[2])
	if strings.Contains(string(raw), `"at"`) || strings.Contains(string(raw), "age_seconds") {
		t.Errorf("a check with no time carries one: %s", raw)
	}
}

// A recipe and a connector of the same name are two sources; only a repeat
// among recipes, or among connectors, is dropped.
func TestScrubSourcesKeepsARecipeAndAConnectorOfTheSameName(t *testing.T) {
	recipeSrc := Source{Name: "acme/wiki", Origin: OriginPlugin, Plugin: "acme", Digest: testDigest, Kind: KindExec, Shape: "feed", Check: SourceCheck{Outcome: CheckNever}}
	conn := Source{Name: "acme/wiki", Origin: OriginPlugin, Plugin: "acme", Digest: testDigest, Kind: KindMCP, Check: SourceCheck{Outcome: CheckUnsupported}}
	got := ScrubSources([]Source{recipeSrc, conn, recipeSrc, conn}, time.Now())
	if len(got) != 2 || got[0].Kind != KindExec || got[1].Kind != KindMCP {
		t.Fatalf("kept %+v, want the recipe and the connector once each", got)
	}
}

func TestScrubSourcesDropsMalformedElements(t *testing.T) {
	ok := Source{Name: "wiki-lister", Origin: OriginOperator, Kind: KindExec, Check: SourceCheck{Outcome: CheckNever}}
	with := func(f func(s *Source)) Source { s := ok; f(&s); return s }
	bad := map[string]Source{
		"unknown origin":             with(func(s *Source) { s.Origin = "kb" }),
		"unknown kind":               with(func(s *Source) { s.Kind = "shell" }),
		"unknown shape":              with(func(s *Source) { s.Shape = "document" }),
		"unknown outcome":            with(func(s *Source) { s.Check.Outcome = "pending" }),
		"name with a space":          with(func(s *Source) { s.Name = "wiki lister" }),
		"name too long":              with(func(s *Source) { s.Name = strings.Repeat("a", 41) }),
		"operator name with a slash": with(func(s *Source) { s.Name = "acme/pages" }),
		"operator carrying a plugin": with(func(s *Source) { s.Plugin = "acme" }),
		"core that is not builtin":   {Name: "git-docs", Origin: OriginCore, Kind: KindExec, Check: SourceCheck{Outcome: CheckNever}},
		"plugin with no digest":      {Name: "acme/pages", Origin: OriginPlugin, Plugin: "acme", Kind: KindExec, Check: SourceCheck{Outcome: CheckNever}},
		"plugin with a short digest": {Name: "acme/pages", Origin: OriginPlugin, Plugin: "acme", Digest: "abc", Kind: KindExec, Check: SourceCheck{Outcome: CheckNever}},
		"plugin not the name's":      {Name: "acme/pages", Origin: OriginPlugin, Plugin: "other", Digest: testDigest, Kind: KindExec, Check: SourceCheck{Outcome: CheckNever}},
		"plugin name unqualified":    {Name: "pages", Origin: OriginPlugin, Plugin: "acme", Digest: testDigest, Kind: KindExec, Check: SourceCheck{Outcome: CheckNever}},
		"a connector with a shape":   {Name: "wiki-mcp", Origin: OriginOperator, Kind: KindMCP, Shape: "feed", Check: SourceCheck{Outcome: CheckUnsupported}},
		"a code on an ok check":      with(func(s *Source) { s.Check = SourceCheck{Outcome: CheckOK, Code: "check_failed"} }),
		"a failed check, no code":    with(func(s *Source) { s.Check = SourceCheck{Outcome: CheckFailed} }),
		"a code that is a sentence":  with(func(s *Source) { s.Check = SourceCheck{Outcome: CheckFailed, Code: "token sk-live-1234 refused"} }),
		"a negative age":             with(func(s *Source) { s.Check = SourceCheck{Outcome: CheckOK, AgeSeconds: -1} }),
		"an absurd age":              with(func(s *Source) { s.Check = SourceCheck{Outcome: CheckOK, AgeSeconds: maxCheckAge + 1} }),
	}
	for name, s := range bad {
		if got := ScrubSources([]Source{s}, time.Now()); len(got) != 0 {
			t.Errorf("%s: kept %+v", name, got)
		}
	}
	// One bad element does not take the good ones with it, and a name repeats once.
	got := ScrubSources([]Source{bad["unknown kind"], ok, ok, bad["unknown origin"]}, time.Now())
	if len(got) != 1 || got[0].Name != "wiki-lister" {
		t.Errorf("kept %+v, want the one good element once", got)
	}
}

func TestScrubSourcesCapsTheArray(t *testing.T) {
	var in []Source
	for i := 0; i < MaxSources+50; i++ {
		name := "r-" + strings.Repeat("a", i/26) + string(rune('a'+i%26))
		in = append(in, Source{Name: name, Origin: OriginOperator, Kind: KindExec, Check: SourceCheck{Outcome: CheckNever}})
	}
	if got := ScrubSources(in, time.Now()); len(got) != MaxSources {
		t.Errorf("kept %d, want the cap %d", len(got), MaxSources)
	}
}
