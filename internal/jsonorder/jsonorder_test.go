package jsonorder

import (
	"strings"
	"testing"
)

func roundTrip(t *testing.T, in string, edit func(map[string]interface{})) string {
	t.Helper()
	m, layout, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if edit != nil {
		edit(m)
	}
	out, err := Marshal(m, layout)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(out)
}

func TestRoundTrip_IsByteIdenticalForAWellFormedFile(t *testing.T) {
	for name, in := range map[string]string{
		"tabs":        "{\n\t\"zeta\": 1,\n\t\"alpha\": {\n\t\t\"b\": [\n\t\t\t1,\n\t\t\t\"x\"\n\t\t],\n\t\t\"a\": null\n\t}\n}\n",
		"two spaces":  "{\n  \"z\": true,\n  \"a\": false\n}\n",
		"no final nl": "{\n  \"z\": 1\n}",
		"crlf":        "{\r\n  \"z\": 1,\r\n  \"a\": 2\r\n}\r\n",
		"empty":       "{}\n",
		"empty parts": "{\n  \"a\": {},\n  \"b\": []\n}\n",
	} {
		if got := roundTrip(t, in, nil); got != in {
			t.Errorf("%s:\n%q\nwant\n%q", name, got, in)
		}
	}
}

func TestParse_NumbersKeepTheirDigitsAndHTMLIsNotEscaped(t *testing.T) {
	in := "{\n  \"n\": 12345678901234567890,\n  \"f\": 1.50,\n  \"c\": \"a && b > c <d>\"\n}\n"
	if got := roundTrip(t, in, nil); got != in {
		t.Errorf("got %q, want %q", got, in)
	}
}

func TestParse_DuplicateKeysKeepTheFirstPositionAndTheLastValue(t *testing.T) {
	got := roundTrip(t, "{\"a\": 1, \"b\": 2, \"a\": 3}", nil)
	if strings.Index(got, "\"a\"") > strings.Index(got, "\"b\"") || !strings.Contains(got, "\"a\": 3") || strings.Count(got, "\"a\"") != 1 {
		t.Errorf("got %q", got)
	}
}

func TestMarshal_AddedKeysGoAfterTheOriginalsSorted(t *testing.T) {
	got := roundTrip(t, "{\n  \"z\": 1,\n  \"a\": 2\n}\n", func(m map[string]interface{}) {
		m["y"] = 1
		m["b"] = 2
		delete(m, "a")
	})
	if got != "{\n  \"z\": 1,\n  \"b\": 2,\n  \"y\": 1\n}\n" {
		t.Errorf("got %q", got)
	}
}

func TestParse_RefusesWhatIsNotAJSONObject(t *testing.T) {
	for _, in := range []string{"[1]", "3", "{", "{} x", ""} {
		if _, _, err := Parse([]byte(in)); err == nil {
			t.Errorf("Parse(%q) must fail", in)
		}
	}
	if m, _, err := Parse([]byte("null")); err != nil || len(m) != 0 {
		t.Errorf("null is an empty object: %v %v", m, err)
	}
}

// Nesting past the cap is refused, so a hostile file cannot make the rewrite
// amplify its size (18 KB of nested arrays became 162 MB).
func TestParse_RefusesDeepNesting(t *testing.T) {
	deep := func(n int) string {
		return "{\"a\":" + strings.Repeat("[", n) + strings.Repeat("]", n) + "}"
	}
	if _, _, err := Parse([]byte(deep(maxJSONDepth - 2))); err != nil {
		t.Errorf("a depth within the cap must parse: %v", err)
	}
	if _, _, err := Parse([]byte(deep(9000))); err == nil || !strings.Contains(err.Error(), "levels") {
		t.Errorf("9000 levels must be refused, got %v", err)
	}
}

func TestMarshal_RefusesDeepNesting(t *testing.T) {
	var v interface{} = "x"
	for i := 0; i < maxJSONDepth+2; i++ {
		v = []interface{}{v}
	}
	if _, err := Marshal(map[string]interface{}{"a": v}, NewLayout()); err == nil {
		t.Error("an in-memory value past the cap must be refused")
	}
}

func TestParse_AHugeOrOddIndentFallsBackToTwoSpaces(t *testing.T) {
	huge := "{\n" + strings.Repeat(" ", 1<<20) + "\"a\": 1\n}\n"
	got := roundTrip(t, huge, nil)
	if got != "{\n  \"a\": 1\n}\n" {
		t.Errorf("got %d bytes, want the 2-space rewrite", len(got))
	}
	// 16 is the longest unit kept.
	ok := "{\n" + strings.Repeat(" ", 16) + "\"a\": 1\n}\n"
	if got := roundTrip(t, ok, nil); got != ok {
		t.Errorf("a 16-space indent is kept: %q", got)
	}
}
