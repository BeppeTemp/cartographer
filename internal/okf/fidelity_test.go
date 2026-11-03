package okf

import (
	"reflect"
	"strings"
	"testing"
)

func mustParse(t *testing.T, raw string) *Frontmatter {
	t.Helper()
	fm, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return fm
}

func TestCheckKey(t *testing.T) {
	for _, k := range []string{"", "a:b", "x\nstatus", "# c", " sp", "sp ", "- x", "a\rb"} {
		fm := mustParse(t, "type: T")
		fm.Set(k, "v")
		err := fm.Validate()
		if err == nil {
			t.Errorf("key %q accepted", k)
		} else if k != "" && !strings.Contains(err.Error(), "key") {
			t.Errorf("key %q: error does not name the key: %v", k, err)
		}
	}
	fm := mustParse(t, "type: T")
	fm.Set("normal_key-1", "v")
	if err := fm.Validate(); err != nil {
		t.Errorf("normal key refused: %v", err)
	}
}

func TestIndentedLineIsNeverAKey(t *testing.T) {
	raw := "type: T\ndescription: x\n  Text with: colon\n  more text\ntitle: x"
	fm := mustParse(t, raw)
	if got, want := fm.Keys(), []string{"type", "description", "title"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	fm.Set("title", "y")
	if out := fm.Serialize(); !strings.Contains(out, "description: x\n  Text with: colon\n  more text\n") {
		t.Errorf("continuation lines moved:\n%s", out)
	}
}

func TestBlockScalars(t *testing.T) {
	cases := []struct{ header, want string }{
		{"|", "line one\nline two\n"},
		{"|-", "line one\nline two"},
		{"|+", "line one\nline two\n"},
		{">", "line one line two\n"},
		{">-", "line one line two"},
	}
	for _, c := range cases {
		raw := "description: " + c.header + "\n  line one\n  line two\ntitle: x"
		fm := mustParse(t, raw)
		if v, _ := fm.Get("description"); v != c.want {
			t.Errorf("%q: got %q, want %q", c.header, v, c.want)
		}
		if got, want := fm.Keys(), []string{"description", "title"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%q: keys %v", c.header, got)
		}
		fm.Set("title", "y")
		if out := fm.Serialize(); out != strings.Replace(raw, "title: x", "title: y", 1) {
			t.Errorf("%q: block not byte-identical:\n%s", c.header, out)
		}
	}
	fm := mustParse(t, "d: >-\n  a: colon\n\n  para\n")
	if v, _ := fm.Get("d"); v != "a: colon\npara" {
		t.Errorf("blank line in folded block: %q", v)
	}
	// Overwriting the value replaces the block.
	fm = mustParse(t, "d: >-\n  a\n  b\nt: x")
	fm.Set("d", "new")
	if out := fm.Serialize(); out != "d: new\nt: x" {
		t.Errorf("overwrite: %q", out)
	}
}

func TestInlineComments(t *testing.T) {
	raw := "status: active # set by doctor\ntitle: \"foo\" # c\ntags: [a, b] # list\nq: \"a # b\"\nn: Fix bug#12\nz: # nothing"
	fm := mustParse(t, raw)
	checkString(t, fm, "status", "active")
	checkString(t, fm, "title", "foo")
	checkString(t, fm, "q", "a # b")
	checkString(t, fm, "n", "Fix bug#12")
	if v, _ := fm.Get("tags"); !reflect.DeepEqual(v, []string{"a", "b"}) {
		t.Errorf("tags = %v", v)
	}
	if v, ok := fm.Get("z"); !ok || v != nil {
		t.Errorf("z = %v", v)
	}
	fm.Set("n", "other")
	out := fm.Serialize()
	for _, want := range []string{"status: active # set by doctor\n", "title: foo # c\n", "tags: [a, b] # list\n", "q: \"a # b\"\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("comment lost, want %q in:\n%s", want, out)
		}
	}
	if err := VerifyRoundTrip(out); err != nil {
		t.Error(err)
	}
}

func TestEdgeWhitespaceRoundTrips(t *testing.T) {
	for _, ws := range []string{" ", "\v", "\f", " "} {
		for _, v := range []string{ws + "foo", "foo" + ws} {
			fm := mustParse(t, "type: T")
			fm.Set("title", v)
			fm.Set("l", []string{v, "x"})
			back := mustParse(t, fm.Serialize())
			if got, _ := back.Get("title"); got != v {
				t.Errorf("scalar %q came back %q", v, got)
			}
			if got, _ := back.Get("l"); !reflect.DeepEqual(got, []string{v, "x"}) {
				t.Errorf("list item %q came back %q", v, got)
			}
		}
	}
}

func TestSplitFrontmatterBOM(t *testing.T) {
	text := "---\ntype: T\n---\nbody\n"
	fm1, b1, ok1 := SplitFrontmatter(text)
	fm2, b2, ok2 := SplitFrontmatter("\xef\xbb\xbf" + text)
	if !ok2 || ok1 != ok2 || fm1 != fm2 || b1 != b2 {
		t.Errorf("BOM changes the split: %q %q %v", fm2, b2, ok2)
	}
	if ContentHash(text) != ContentHash("\xef\xbb\xbf"+text) {
		t.Error("BOM changes the content hash")
	}
}

func TestCommentsSurviveOnBlocks(t *testing.T) {
	raw := "description: > # note\n  a\n  b\np: # c\n  codex: x\nt: x"
	fm := mustParse(t, raw)
	fm.Set("t", "y")
	if got, want := fm.Serialize(), strings.Replace(raw, "t: x", "t: y", 1); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestParsedKeysStayWritable(t *testing.T) {
	fm := mustParse(t, "type: T\na#b: v\n- name: a\ntitle: x")
	fm.Set("title", "y")
	if err := fm.Validate(); err != nil {
		t.Fatalf("existing keys refused: %v", err)
	}
	fm.Set("c:d", "v")
	if err := fm.Validate(); err == nil {
		t.Fatal("a set key with ':' accepted")
	}
}
