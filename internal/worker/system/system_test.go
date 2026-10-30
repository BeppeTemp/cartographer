package system

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/worker/recipe"
)

func ents(kv ...string) []recipe.Entry {
	var out []recipe.Entry
	for i := 0; i < len(kv); i += 2 {
		out = append(out, recipe.Entry{Key: kv[i], Content: kv[i+1]})
	}
	return out
}

func TestRevisionFoldsCRLFAndIgnoresOrder(t *testing.T) {
	if Revision("a\r\nb\r\n") != Revision("a\nb\n") || Revision("a") == Revision("b") || len(Revision("a")) != 16 {
		t.Fatal("revision")
	}
	g1, err := ValidateListing(ents("ns/b", "2", "ns/a", "1"), 100)
	g2, _ := ValidateListing(ents("ns/a", "1", "ns/b", "2"), 100)
	if err != nil || g1[0].Key != "ns/a" || g1[0] != g2[0] || g1[1] != g2[1] {
		t.Fatalf("%v %v", g1, err)
	}
}

func TestKeyRules(t *testing.T) {
	for _, k := range []string{"namespace/team-a", "a", "A.b_c:d/e@f-g", strings.Repeat("a", 200)} {
		if !ValidKey(k) {
			t.Errorf("%q refused", k)
		}
	}
	for _, k := range []string{"", "no", "No", "true", "null", "123", "1a", "a=b", "a b", "-a", strings.Repeat("a", 201), "é"} {
		if ValidKey(k) {
			t.Errorf("%q accepted", k)
		}
	}
}

func TestValidateListingViolations(t *testing.T) {
	big := make([]recipe.Entry, MaxGroups+1)
	for i := range big {
		big[i] = recipe.Entry{Key: "k" + strings.Repeat("a", i%5) + string(rune('a'+i%26)), Content: "x"}
	}
	for name, tc := range map[string]struct {
		in   []recipe.Entry
		want string
	}{
		"bad key":  {ents("no", "x"), `"no"`},
		"dup":      {ents("a", "x", "a", "y"), "twice"},
		"empty":    {ents("a", ""), `"a"`},
		"nul":      {ents("a", "x\x00"), `"a"`},
		"utf8":     {ents("a", "\xff"), `"a"`},
		"over cap": {ents("big", strings.Repeat("x", 11)), `"big"`},
		"too many": {big, "1001 groups"},
	} {
		_, err := ValidateListing(tc.in, 10)
		var e *recipe.Error
		if !errors.As(err, &e) || e.Code != recipe.CodeProtocolError || !strings.Contains(e.Detail, tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	g, err := ValidateListing([]recipe.Entry{{Key: "a", Title: "T\x1b\n" + strings.Repeat("é", 400), Locator: "l\x00", Content: "x"}}, 10)
	if err != nil || len([]rune(g[0].Title)) != 300 || strings.ContainsAny(g[0].Title+g[0].Locator, "\x1b\n\x00") {
		t.Errorf("labels: %+v %v", g, err)
	}
	if g, _ := ValidateListing(ents("a", "x"), 10); g[0].Title != "a" {
		t.Error("title defaults to the key")
	}
}

func sampleState(t *testing.T) State {
	g, err := ValidateListing(ents("ns/b", "two <&> \"q\"\nlines", "ns/a", "one"), 100)
	if err != nil {
		t.Fatal(err)
	}
	return State{}.Apply([]Handled{{Key: "ns/b", Group: g[1], At: "2031-02-03T04:05:06Z"}, {Key: "ns/a", Group: g[0], At: "2031-02-03T04:05:06Z"}})
}

func TestStateRoundTripAndDeterminism(t *testing.T) {
	s := sampleState(t)
	b1, d1, err := s.Encode()
	b2, d2, _ := sampleState(t).Encode()
	if err != nil || string(b1) != string(b2) || d1 != d2 || d1 != Digest(b1) {
		t.Fatal("not deterministic")
	}
	if !strings.HasPrefix(string(b1), "{\"version\":1}\n{\"key\":\"ns/a\"") || strings.Contains(string(b1), "\r") || strings.Contains(string(b1), `\u0026`) {
		t.Errorf("bytes = %q", b1)
	}
	back, err := Decode(b1)
	if err != nil || len(back) != 2 || back["ns/b"].Snapshot != s["ns/b"].Snapshot {
		t.Fatalf("round trip: %v %v", back, err)
	}
	if _, _, err := (State{"a": {Snapshot: strings.Repeat("x", MaxStateBytes)}}).Encode(); err == nil {
		t.Error("an oversized state was encoded")
	}
}

func TestDecodeRejectsEveryCorruption(t *testing.T) {
	good, _, _ := sampleState(t).Encode()
	line := func(s string) string { return strings.Split(s, "\n")[1] }(string(good))
	hdr := "{\"version\":1}\n"
	for name, in := range map[string]string{
		"empty":        "",
		"no header":    line + "\n",
		"version 2":    "{\"version\":2}\n" + line + "\n",
		"no newline":   hdr + line,
		"malformed":    hdr + "{\"key\":\n",
		"dup":          hdr + line + "\n" + line + "\n",
		"bad revision": hdr + strings.Replace(line, `"revision":"`, `"revision":"zz`, 1) + "\n",
		"bad key":      hdr + strings.Replace(line, `"ns/a"`, `"no"`, 1) + "\n",
		"unknown":      hdr + strings.Replace(line, `"key"`, `"x":1,"key"`, 1) + "\n",
		"two objects":  hdr + line + line + "\n",
	} {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestPlan(t *testing.T) {
	prev := sampleState(t)
	cur, _ := ValidateListing(ents("ns/a", "one", "ns/b", "changed", "ns/c", "new"), 100)
	c, err := Plan(prev, cur)
	if err != nil || len(c.Added) != 1 || c.Added[0].Key != "ns/c" || len(c.Modified) != 1 || c.Modified[0].Previous.Snapshot == "" || c.Unchanged != 1 || len(c.Removed) != 0 {
		t.Fatalf("%+v %v", c, err)
	}
	cur2, _ := ValidateListing(ents("ns/a", "one"), 100)
	c, _ = Plan(prev, cur2)
	if len(c.Removed) != 1 || c.Removed[0].Key != "ns/b" || c.Removed[0].Previous.Revision == "" || len(c.Ordered()) != 1 {
		t.Fatalf("removed: %+v", c)
	}
	if _, err := Plan(prev, nil); !errors.Is(err, ErrEmptyListing) {
		t.Errorf("empty = %v", err)
	}
	if c, err := Plan(State{}, nil); err != nil || len(c.Ordered()) != 0 {
		t.Errorf("empty on empty: %v", err)
	}
	if c, _ := Plan(prev, mustGroups(t, "ns/a", "one", "ns/b", "two <&> \"q\"\nlines")); len(c.Ordered()) != 0 || c.Unchanged != 2 {
		t.Errorf("unchanged: %+v", c)
	}
}

func mustGroups(t *testing.T, kv ...string) []Group {
	g, err := ValidateListing(ents(kv...), 100)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestApplyKeepsOldEntriesOfFailedAndCappedGroups(t *testing.T) {
	prev := sampleState(t)
	cur := mustGroups(t, "ns/a", "changed", "ns/b", "changed too", "ns/c", "new", "ns/d", "newer")
	n := prev.Apply([]Handled{{Key: "ns/a", Group: cur[0], At: "t"}, {Key: "ns/c", Group: cur[2], At: "t"}})
	if n["ns/a"].Snapshot != "changed" || n["ns/b"].Snapshot != prev["ns/b"].Snapshot || n["ns/c"].Snapshot != "new" || len(n) != 3 {
		t.Errorf("apply = %+v", n)
	}
	if prev["ns/a"].Snapshot != "one" {
		t.Error("Apply mutated its receiver")
	}
	if n = prev.Apply([]Handled{{Key: "ns/b", Removed: true}}); len(n) != 1 || n["ns/b"].Key != "" {
		t.Errorf("removal: %+v", n)
	}
}

func TestLineDiffGoldens(t *testing.T) {
	for name, tc := range map[string]struct{ a, b, want string }{
		"identical": {"a\nb\n", "a\nb\n", ""},
		"insert":    {"a\nc\n", "a\nb\nc\n", " a\n+b\n c"},
		"delete":    {"a\nb\nc\n", "a\nc\n", " a\n-b\n c"},
		"replace":   {"a\nb\nc\n", "a\nX\nc\n", " a\n-b\n+X\n c"},
		"crlf":      {"a\r\nb\r\n", "a\nb\n", ""},
		"first":     {"", "a\nb\n", "+a\n+b"},
		"gap":       {"1\n2\n3\n4\n5\n6\n7\n8\n", "1\n2\n3\n4\n5\n6\n7\nX\n", " 6\n 7\n-8\n+X"},
	} {
		if got := LineDiff(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
	if got := LineDiff("1\n2\n3\n4\n5\n6\n7\n8\n9\n", "X\n2\n3\n4\n5\n6\n7\n8\nY\n"); got != "-1\n+X\n 2\n 3\n...\n 7\n 8\n-9\n+Y" {
		t.Errorf("two hunks: %q", got)
	}
	if got := LineDiff(strings.Repeat("a\n", 2100), strings.Repeat("b\n", 2100)); got != "diff omitted: group too large to compare" {
		t.Errorf("omitted: %q", got)
	}
	long := LineDiff("", strings.Repeat("line\n", 500))
	if n := strings.Count(long, "\n"); n != 400 || !strings.HasSuffix(long, "[diff truncated: 100 more lines]") {
		t.Errorf("truncated at %d lines: %q", n, long[len(long)-40:])
	}
	wide := LineDiff("", strings.Repeat(strings.Repeat("x", 1000)+"\n", 100))
	if len(wide) > maxDiffBytes+60 || !strings.Contains(wide, "[diff truncated:") {
		t.Errorf("byte cap: %d", len(wide))
	}
	if LineDiff("a\nb", "a\nb") != "" {
		t.Error("identical without a final newline")
	}
}

func TestUnitIDStabilityDayAndFlap(t *testing.T) {
	d1, d2, d3 := time.Date(2031, 2, 3, 23, 59, 0, 0, time.UTC), time.Date(2031, 2, 4, 0, 1, 0, 0, time.UTC), time.Date(2031, 2, 5, 9, 0, 0, 0, time.UTC)
	rA, rB := Revision("a"), Revision("b")
	a1 := UnitID("ingest", "src/s", "namespace/Team-A", rA, d1)
	if a1 != UnitID("ingest", "src/s", "namespace/Team-A", rA, d1.Add(-time.Hour)) {
		t.Error("not stable within a day")
	}
	b := UnitID("ingest", "src/s", "namespace/Team-A", rB, d2)
	a2 := UnitID("ingest", "src/s", "namespace/Team-A", rA, d3)
	if a1 == b || b == a2 || a1 == a2 {
		t.Errorf("A -> B -> A: %s %s %s", a1, b, a2)
	}
	if !strings.HasPrefix(a1, "ingest/namespace-team-a-") || len(a1) != len("ingest/namespace-team-a-")+8 {
		t.Errorf("shape = %s", a1)
	}
	if UnitID("ingest", "src/s", "namespace/Team-A", RemovedRevision(rA), d1) == a1 {
		t.Error("a removal shares the added ID")
	}
	if got := UnitID("m", "s", strings.Repeat("ab-", 30), rA, d1); len(strings.TrimPrefix(got, "m/")) != 40+1+8 && !strings.Contains(got, "-") {
		t.Errorf("long slug: %s", got)
	}
}

// The worker's own slug must be one the server accepts (checkConceptName).
func TestUnitIDIsAServerSlug(t *testing.T) {
	day := time.Date(2031, 2, 3, 0, 0, 0, 0, time.UTC)
	for _, key := range []string{"namespace/team-a", "A", "a.b_c:d/e@f--g", strings.Repeat("x-", 60), "Zeta_9/..x", "a" + strings.Repeat("-", 39) + "b"} {
		id := UnitID("ingest", "src/s", key, Revision(key), day)
		stem := id[strings.LastIndex(id, "/")+1:]
		if kb.Slugify(stem) != stem || !kb.SlugPattern.MatchString(stem) {
			t.Errorf("%q: stem %q is not a slug (Slugify gives %q)", key, stem, kb.Slugify(stem))
		}
		if got, err := okf.PathToID(id + ".md"); err != nil || string(got) != id {
			t.Errorf("%q: PathToID = %v, %v", id, got, err)
		}
	}
}
